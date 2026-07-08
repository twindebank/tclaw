package telegramchannel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tclaw/internal/channel"
	tgsdk "tclaw/internal/telegram"
)

// maxMediaDownloadBytes is the maximum file size we'll download from Telegram.
// 20 MiB is the Telegram Bot API's own hard cap for getFile downloads, so
// nothing larger is reachable anyway; the download streams to disk so this
// bound never has to fit in memory on the 512MB Fly VM.
const maxMediaDownloadBytes = 20 * 1024 * 1024

// mediaRetention is how long downloaded media files are kept before cleanup.
// Files older than this are deleted when new media is downloaded.
const mediaRetention = 24 * time.Hour

// replySnippetMaxLen caps the "[replying to: ...]" preview prepended when a
// user replies to a prior message. Large enough to cover a multi-line summary
// (e.g. a no_reply cross-channel digest) rather than just the opening words.
const replySnippetMaxLen = 500

// TelegramOptions configures optional webhook mode for a Telegram channel.
type TelegramOptions struct {
	// WebhookURL is the full URL Telegram should POST updates to.
	// If empty, the bot uses long polling instead.
	WebhookURL string

	// WebhookPath is the local HTTP path to register the handler at (e.g. "/telegram/abc123").
	// Must match the path component of WebhookURL so the HTTP server routes
	// Telegram's POSTs to the correct handler.
	WebhookPath string

	// RegisterHandler adds an HTTP handler to the shared server.
	// Required when WebhookURL is set.
	RegisterHandler func(pattern string, handler http.Handler)

	// ChatID seeds the chat ID so the bot can send outbound messages
	// before any inbound message arrives (e.g. schedule-fired prompts).
	ChatID int64

	// OnChatID is called when the chat ID is first set or changes.
	OnChatID func(chatID int64)

	// MediaDir is the directory where downloaded media files are saved.
	// Must be inside the agent's sandbox (e.g. memory/media/).
	// When empty, media messages are processed as text-only (caption only).
	MediaDir string
}

// Telegram connects to the Telegram Bot API using either long polling or webhooks.
// Each incoming message is forwarded to the agent; responses are sent
// back via sendMessage / editMessageText.
//
// The bot instance is created once in Messages() and stored for use by
// Send/Edit. This means Messages() must be called before Send/Edit
// (which the router guarantees — channels start listening before the agent runs).
type Telegram struct {
	token       string
	name        string
	description string
	purpose     string
	opts        TelegramOptions

	// allowedUsers restricts which Telegram user IDs can interact with this bot.
	// When non-empty, messages from users not in this set are silently ignored.
	allowedUsers map[int64]struct{}

	// webhookSecret is a random token used to verify that incoming webhook
	// requests are actually from Telegram, not a third party who guessed the URL.
	// Generated at construction time, passed to both SetWebhook (so Telegram sends it)
	// and WithWebhookSecretToken (so the library checks it on every request).
	webhookSecret string

	mu            sync.Mutex
	currentChatID int64
	bot           *bot.Bot // set in Messages(), used by Send/Edit
}

func NewTelegram(token, name, description, purpose string, allowedUsers []int64, opts TelegramOptions) *Telegram {
	allowed := make(map[int64]struct{}, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = struct{}{}
	}

	return &Telegram{
		token:         token,
		name:          name,
		description:   description,
		purpose:       purpose,
		opts:          opts,
		allowedUsers:  allowed,
		webhookSecret: generateWebhookSecret(),
		currentChatID: opts.ChatID,
	}
}

// generateWebhookSecret returns a 32-char hex string (128 bits of entropy).
func generateWebhookSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; if it does something is very wrong.
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

func (t *Telegram) Info() channel.Info {
	return channel.Info{
		ID:          channel.ChannelID("telegram:" + t.name),
		Type:        channel.TypeTelegram,
		Name:        t.name,
		Description: t.description,
		Purpose:     t.purpose,
	}
}

func (t *Telegram) Messages(ctx context.Context) <-chan string {
	out := make(chan string)

	go func() {
		defer close(out)

		opts := []bot.Option{
			bot.WithDefaultHandler(func(handlerCtx context.Context, b *bot.Bot, update *models.Update) {
				if update.Message == nil {
					return
				}
				msg := update.Message

				// Extract text — media messages use Caption, text messages use Text.
				text := msg.Text
				if text == "" {
					text = msg.Caption
				}

				attachment, hasMedia := mediaFileInfo(msg)
				if text == "" && !hasMedia {
					return
				}

				// Reject messages from users not in the allowlist.
				if len(t.allowedUsers) > 0 {
					fromID := int64(0)
					if msg.From != nil {
						fromID = msg.From.ID
					}
					if _, ok := t.allowedUsers[fromID]; !ok {
						slog.Warn("telegram message from unauthorized user",
							"from_id", fromID,
							"channel", t.name,
						)
						return
					}
				}

				chatID := msg.Chat.ID

				// Download media if present.
				if hasMedia && t.opts.MediaDir != "" {
					mediaPath, err := t.downloadMedia(handlerCtx, b, msg, attachment)
					if err != nil {
						slog.Error("failed to download media", "err", err, "channel", t.name)
						text = formatMediaError(text, err)
					} else {
						text = formatMediaMessage(text, mediaPath)
					}
				}

				// Prepend a short snippet of the replied-to message so the
				// agent knows what the user is referring to.
				if reply := msg.ReplyToMessage; reply != nil && reply.Text != "" {
					snippet := tgsdk.TruncateSnippet(reply.Text, replySnippetMaxLen)
					text = "[replying to: \"" + snippet + "\"]\n" + text
				}

				slog.Info("telegram message received",
					"chat_id", chatID,
					"length", len(text),
					"has_media", hasMedia,
					"channel", t.name,
				)

				t.mu.Lock()
				changed := t.currentChatID != chatID
				t.currentChatID = chatID
				t.mu.Unlock()

				if changed && t.opts.OnChatID != nil {
					t.opts.OnChatID(chatID)
				}

				select {
				case out <- text:
				case <-ctx.Done():
				case <-time.After(30 * time.Second):
					// The message pipeline is backed up — the agent is busy and
					// the bridge channel is full. Drop the message rather than
					// blocking the bot worker indefinitely, which would prevent
					// processing of any further webhook updates.
					slog.Warn("telegram message dropped, pipeline blocked",
						"channel", t.name, "length", len(text))
				}
			}),
			// Process messages sequentially so we don't interleave responses.
			bot.WithNotAsyncHandlers(),
		}

		// In webhook mode, verify the secret token on every incoming request
		// to reject forged updates from third parties.
		if t.opts.WebhookURL != "" {
			opts = append(opts, bot.WithWebhookSecretToken(t.webhookSecret))
		}

		b, err := bot.New(t.token, opts...)
		if err != nil {
			slog.Error("failed to create telegram bot", "err", err, "channel", t.name)
			return
		}

		t.mu.Lock()
		t.bot = b
		t.mu.Unlock()

		if t.opts.WebhookURL != "" {
			t.startWebhook(ctx, b)
		} else {
			t.startPolling(ctx, b)
		}
	}()

	return out
}

// startPolling uses long polling to receive updates (local dev).
func (t *Telegram) startPolling(ctx context.Context, b *bot.Bot) {
	slog.Debug("telegram bot starting (polling)", "channel", t.name)
	b.Start(ctx)
	slog.Debug("telegram bot stopped", "channel", t.name)
}

// startWebhook registers a webhook with Telegram and serves updates via HTTP.
func (t *Telegram) startWebhook(ctx context.Context, b *bot.Bot) {
	// Register the HTTP handler at the same path the router told Telegram to POST to.
	t.opts.RegisterHandler(t.opts.WebhookPath, b.WebhookHandler())

	if !t.registerWebhook(ctx, b) {
		// Webhook never registered — the bot would receive no updates.
		// Logged inside registerWebhook; bail out so we don't leave a
		// silently-dead bot consuming the goroutine.
		return
	}

	// StartWebhook processes updates from the internal channel (fed by WebhookHandler).
	// It blocks until ctx is cancelled.
	slog.Debug("telegram bot starting (webhook)", "channel", t.name)
	b.StartWebhook(ctx)
	slog.Info("telegram bot stopped", "channel", t.name)
}

// webhookSetupRetries bounds how long we'll keep retrying setWebhook on a
// rate-limit response. With backoff capped at 30s, this works out to a few
// minutes of patience — enough to ride out a deploy storm where every channel
// re-registers its webhook simultaneously, without hanging forever if the
// upstream stays broken.
const webhookSetupRetries = 6

// registerWebhook calls SetWebhook with retry-after-aware retries so a deploy
// that re-registers every channel in parallel doesn't permanently disable bots
// that lose the rate-limit race. Returns true once Telegram has accepted the
// webhook (or the context is cancelled — in which case the bot is going away
// anyway).
func (t *Telegram) registerWebhook(ctx context.Context, b *bot.Bot) bool {
	params := &bot.SetWebhookParams{
		URL:                t.opts.WebhookURL,
		DropPendingUpdates: true,
		SecretToken:        t.webhookSecret,
		// Limit Telegram to 1 concurrent connection per bot. The default (40)
		// causes many idle keep-alive connections that count toward Fly's
		// concurrency limit. We process messages sequentially anyway.
		MaxConnections: 1,
	}

	for attempt := 0; attempt < webhookSetupRetries; attempt++ {
		ok, err := b.SetWebhook(ctx, params)
		if err == nil {
			slog.Debug("telegram webhook registered",
				"channel", t.name, "url", t.opts.WebhookURL, "ok", ok)
			return true
		}

		tooMany, isRateLimit := err.(*bot.TooManyRequestsError)
		if !isRateLimit {
			slog.Error("failed to set telegram webhook",
				"err", err, "channel", t.name, "url", t.opts.WebhookURL)
			return false
		}
		retryAfter := 2 * time.Second
		if tooMany.RetryAfter > 0 {
			retryAfter = time.Duration(tooMany.RetryAfter) * time.Second
		}

		slog.Warn("telegram webhook rate-limited, retrying",
			"channel", t.name, "attempt", attempt+1, "retry_after", retryAfter)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(retryAfter):
		}
	}

	slog.Error("failed to set telegram webhook: exhausted rate-limit retries",
		"channel", t.name, "url", t.opts.WebhookURL, "attempts", webhookSetupRetries)
	return false
}

func (t *Telegram) Send(ctx context.Context, text string, opts channel.SendOpts) (channel.MessageID, error) {
	t.mu.Lock()
	chatID := t.currentChatID
	b := t.bot
	t.mu.Unlock()

	if chatID == 0 {
		return "", fmt.Errorf("telegram send: no chat ID set — channel %q has not received an inbound message yet", t.name)
	}

	// Create a send-only bot if Messages() hasn't been called yet (e.g. lifecycle notifications).
	if b == nil {
		var err error
		b, err = bot.New(t.token)
		if err != nil {
			return "", fmt.Errorf("telegram send (create bot): %w", err)
		}
	}

	// Telegram rejects messages containing invalid UTF-8 with a 400 error that
	// will never succeed on retry. Drop invalid bytes rather than losing the message.
	if !utf8.ValidString(text) {
		slog.Warn("telegram send: stripping invalid UTF-8 bytes from outbound message", "channel", t.name)
		text = tgsdk.SanitizeUTF8(text)
	}

	// Allowlist: only messages explicitly marked Notify ring the user.
	// Status, thinking, and lifecycle chatter land silently.
	silent := !opts.Notify

	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:              chatID,
		Text:                tgsdk.SanitizeHTML(tgsdk.MarkdownToHTML(text)),
		ParseMode:           models.ParseModeHTML,
		DisableNotification: silent,
	})
	if err != nil && isHTMLParseError(err) {
		// Malformed HTML — fall back to plain text so the message still reaches the user.
		slog.Warn("telegram send: HTML parse error, falling back to plain text",
			"channel", t.name, "error", err)
		msg, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:              chatID,
			Text:                "⚠️ [formatting error — sent as plain text]\n\n" + tgsdk.StripAllTags(text),
			DisableNotification: silent,
		})
	}
	if err != nil {
		return "", fmt.Errorf("telegram send: %w", err)
	}

	return channel.MessageID(strconv.Itoa(msg.ID)), nil
}

func (t *Telegram) Edit(ctx context.Context, msgID channel.MessageID, text string) error {
	t.mu.Lock()
	chatID := t.currentChatID
	b := t.bot
	t.mu.Unlock()

	if chatID == 0 {
		return fmt.Errorf("telegram edit: no chat ID set — channel %q has not received an inbound message yet", t.name)
	}

	if b == nil {
		var err error
		b, err = bot.New(t.token)
		if err != nil {
			return fmt.Errorf("telegram edit (create bot): %w", err)
		}
	}

	// Telegram rejects messages containing invalid UTF-8 with a 400 error that
	// will never succeed on retry. Drop invalid bytes rather than losing the message.
	if !utf8.ValidString(text) {
		slog.Warn("telegram edit: stripping invalid UTF-8 bytes from outbound message", "channel", t.name)
		text = tgsdk.SanitizeUTF8(text)
	}

	telegramMsgID, err := strconv.Atoi(string(msgID))
	if err != nil {
		return fmt.Errorf("invalid telegram message id %q: %w", msgID, err)
	}

	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: telegramMsgID,
		Text:      tgsdk.SanitizeHTML(tgsdk.MarkdownToHTML(text)),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil && isHTMLParseError(err) {
		// Malformed HTML — fall back to plain text so the message still reaches the user.
		slog.Warn("telegram edit: HTML parse error, falling back to plain text",
			"channel", t.name, "error", err)
		_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: telegramMsgID,
			Text:      "⚠️ [formatting error — sent as plain text]\n\n" + tgsdk.StripAllTags(text),
		})
	}
	if err != nil {
		return fmt.Errorf("telegram edit: %w", err)
	}

	return nil
}

func (t *Telegram) Done(_ context.Context) error {
	return nil
}

// isHTMLParseError returns true when Telegram rejected the message because
// the HTML markup is structurally invalid. Retrying with the same markup
// will never succeed — the caller should fall back to plain text.
func isHTMLParseError(err error) bool {
	return strings.Contains(err.Error(), "can't parse entities")
}

func (t *Telegram) SplitStatusMessages() bool {
	return true
}

func (t *Telegram) Markup() channel.Markup {
	return channel.MarkupHTML
}

func (t *Telegram) StatusWrap() channel.StatusWrap {
	return channel.StatusWrap{Open: "<blockquote expandable>", Close: "</blockquote>"}
}

// downloadMedia downloads the resolved media attachment to the configured
// MediaDir, streaming to disk so a large file never has to fit in memory.
// Returns the absolute path so the agent can pass it directly to the Read tool
// without needing to resolve against a base dir.
func (t *Telegram) downloadMedia(ctx context.Context, b *bot.Bot, msg *models.Message, att mediaAttachment) (string, error) {
	// Clean up old media files before downloading new ones.
	cleanupOldMedia(t.opts.MediaDir)

	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: att.FileID})
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}
	if file.FileSize > maxMediaDownloadBytes {
		return "", fmt.Errorf("file too large (%d bytes, max %d)", file.FileSize, maxMediaDownloadBytes)
	}

	downloadURL := b.FileDownloadLink(file)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download status %d", resp.StatusCode)
	}

	fullPath := filepath.Join(t.opts.MediaDir, mediaFilename(att, msg.ID))
	out, err := os.OpenFile(fullPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	// Bound the copy even when getFile under-reports FileSize (it can be unset
	// for some types) — read one byte past the cap so we can detect overrun.
	written, err := io.Copy(out, io.LimitReader(resp.Body, maxMediaDownloadBytes+1))
	if err != nil {
		removePartialDownload(fullPath)
		return "", fmt.Errorf("write file: %w", err)
	}
	if written > maxMediaDownloadBytes {
		removePartialDownload(fullPath)
		return "", fmt.Errorf("file too large (downloaded %d bytes, max %d)", written, maxMediaDownloadBytes)
	}

	// Return the absolute path so the agent can pass it directly to the Read
	// tool without needing to resolve it relative to any base directory.
	return fullPath, nil
}

// removePartialDownload deletes a half-written download so a rejected file never
// lingers for the agent to stumble onto. Best-effort — logged, never surfaced.
func removePartialDownload(path string) {
	if err := os.Remove(path); err != nil {
		slog.Warn("failed to remove partial media download", "path", path, "err", err)
	}
}

// mediaAttachment is the single resolved view of whatever downloadable file a
// Telegram message carries — the one place that knows the supported type set,
// so the inbound gate, filename, and download all agree.
type mediaAttachment struct {
	// FileID is Telegram's handle for fetching the file via getFile.
	FileID string

	// Ext is the file extension to save under (leading dot, e.g. ".pdf").
	Ext string

	// Prefix is the human-readable filename prefix (e.g. "photo", "document").
	Prefix string
}

// mediaFileInfo resolves the downloadable attachment on a message. ok is false
// when the message carries no supported media (text-only, or an unsupported
// non-file type like a location or poll).
func mediaFileInfo(msg *models.Message) (att mediaAttachment, ok bool) {
	switch {
	case len(msg.Photo) > 0:
		// Telegram sends photos as an array of sizes — last is largest.
		largest := msg.Photo[len(msg.Photo)-1]
		return mediaAttachment{FileID: largest.FileID, Ext: ".jpg", Prefix: "photo"}, true
	case msg.Voice != nil:
		return mediaAttachment{FileID: msg.Voice.FileID, Ext: ".ogg", Prefix: "voice"}, true
	case msg.Audio != nil:
		return mediaAttachment{FileID: msg.Audio.FileID, Ext: extOrDefault(msg.Audio.FileName, ".mp3"), Prefix: "audio"}, true
	case msg.Video != nil:
		return mediaAttachment{FileID: msg.Video.FileID, Ext: extOrDefault(msg.Video.FileName, ".mp4"), Prefix: "video"}, true
	case msg.VideoNote != nil:
		// Round video messages carry no filename; they're always MP4.
		return mediaAttachment{FileID: msg.VideoNote.FileID, Ext: ".mp4", Prefix: "videonote"}, true
	case msg.Animation != nil:
		// Telegram sets both Animation and Document for GIFs; match Animation
		// first so it keeps its ".mp4" (Telegram delivers GIFs as silent MP4).
		return mediaAttachment{FileID: msg.Animation.FileID, Ext: extOrDefault(msg.Animation.FileName, ".mp4"), Prefix: "animation"}, true
	case msg.Sticker != nil:
		return mediaAttachment{FileID: msg.Sticker.FileID, Ext: stickerExt(msg.Sticker), Prefix: "sticker"}, true
	case msg.Document != nil:
		// The catch-all: any file type the user sends as a document.
		return mediaAttachment{FileID: msg.Document.FileID, Ext: extOrDefault(msg.Document.FileName, ".bin"), Prefix: "document"}, true
	default:
		return mediaAttachment{}, false
	}
}

// stickerExt picks the on-disk extension for a sticker: video stickers are
// WebM, animated (Lottie) stickers are gzipped .tgs, and the rest are WebP.
func stickerExt(s *models.Sticker) string {
	switch {
	case s.IsVideo:
		return ".webm"
	case s.IsAnimated:
		return ".tgs"
	default:
		return ".webp"
	}
}

// extOrDefault returns the extension of fileName when it has one, else fallback.
func extOrDefault(fileName, fallback string) string {
	if fileName != "" {
		if e := filepath.Ext(fileName); e != "" {
			return e
		}
	}
	return fallback
}

// mediaFilename builds a unique filename for a downloaded media file, using the
// message ID as a simple collision-resistant suffix.
func mediaFilename(att mediaAttachment, msgID int) string {
	return fmt.Sprintf("%s_%d_%d%s", att.Prefix, time.Now().Unix(), msgID, att.Ext)
}

// formatMediaError builds the prompt text that tells the agent a media
// attachment failed to download so it can inform the user.
func formatMediaError(text string, err error) string {
	notice := fmt.Sprintf("[Attached media could not be downloaded: %v]", err)
	if text == "" {
		return notice
	}
	return notice + "\n" + text
}

// formatMediaMessage builds the prompt text that tells the agent about an
// attached media file. It only ever passes the path — never the contents — so
// the agent decides whether to load the file into context.
func formatMediaMessage(text string, mediaPath string) string {
	mediaType := "file"
	// The Read tool can open images, audio, PDFs, and text; anything else the
	// agent has to reach for another tool, so don't nudge it toward Read.
	hint := "available on disk if you want to inspect it"
	ext := strings.ToLower(filepath.Ext(mediaPath))
	switch {
	case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp":
		mediaType = "image"
		hint = "view it with the Read tool"
	case ext == ".ogg" || ext == ".mp3" || ext == ".m4a" || ext == ".wav" || ext == ".flac":
		mediaType = "audio"
		hint = "view it with the Read tool"
	case ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".mkv" || ext == ".avi":
		mediaType = "video"
	case ext == ".pdf":
		mediaType = "PDF"
		hint = "view it with the Read tool"
	}

	attachment := fmt.Sprintf("[Attached %s: %s — %s]", mediaType, mediaPath, hint)
	if text == "" {
		return attachment
	}
	return attachment + "\n" + text
}

// cleanupOldMedia removes files in the media directory that are older than
// mediaRetention. Best-effort — errors are logged.
func cleanupOldMedia(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("failed to read media dir for cleanup", "dir", dir, "err", err)
		return
	}
	cutoff := time.Now().Add(-mediaRetention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, entry.Name())
			if err := os.Remove(path); err != nil {
				slog.Warn("failed to clean up old media file", "path", path, "err", err)
			}
		}
	}
}
