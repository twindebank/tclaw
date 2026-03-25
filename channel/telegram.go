package channel

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// maxMediaDownloadBytes is the maximum file size we'll download from Telegram.
// Conservative limit for the 512MB Fly VM.
const maxMediaDownloadBytes = 10 * 1024 * 1024

// mediaRetention is how long downloaded media files are kept before cleanup.
// Files older than this are deleted when new media is downloaded.
const mediaRetention = 24 * time.Hour

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
	source      Source
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

func NewTelegram(token, name, description string, allowedUsers []int64, opts TelegramOptions) *Telegram {
	return newTelegram(token, name, description, allowedUsers, SourceStatic, opts)
}

func NewDynamicTelegram(token, name, description string, allowedUsers []int64, opts TelegramOptions) *Telegram {
	return newTelegram(token, name, description, allowedUsers, SourceDynamic, opts)
}

func newTelegram(token, name, description string, allowedUsers []int64, source Source, opts TelegramOptions) *Telegram {
	allowed := make(map[int64]struct{}, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = struct{}{}
	}

	return &Telegram{
		token:         token,
		name:          name,
		description:   description,
		source:        source,
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

func (t *Telegram) Info() Info {
	return Info{
		ID:          ChannelID("telegram:" + t.name),
		Type:        TypeTelegram,
		Name:        t.name,
		Description: t.description,
		Source:      t.source,
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

				hasMedia := len(msg.Photo) > 0 || msg.Voice != nil || msg.Audio != nil
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
					mediaPath, err := t.downloadMedia(handlerCtx, b, msg)
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
					snippet := truncateReplySnippet(reply.Text, 100)
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
	slog.Info("telegram bot starting (polling)", "channel", t.name)
	b.Start(ctx)
	slog.Info("telegram bot stopped", "channel", t.name)
}

// startWebhook registers a webhook with Telegram and serves updates via HTTP.
func (t *Telegram) startWebhook(ctx context.Context, b *bot.Bot) {
	// Register the HTTP handler at the same path the router told Telegram to POST to.
	t.opts.RegisterHandler(t.opts.WebhookPath, b.WebhookHandler())

	// Tell Telegram to send updates to our webhook URL.
	// SecretToken tells Telegram to include this value in the
	// X-Telegram-Bot-Api-Secret-Token header on every webhook POST.
	ok, err := b.SetWebhook(ctx, &bot.SetWebhookParams{
		URL:                t.opts.WebhookURL,
		DropPendingUpdates: true,
		SecretToken:        t.webhookSecret,
	})
	if err != nil {
		slog.Error("failed to set telegram webhook", "err", err, "channel", t.name, "url", t.opts.WebhookURL)
		return
	}
	slog.Info("telegram webhook registered", "channel", t.name, "url", t.opts.WebhookURL, "ok", ok)

	// StartWebhook processes updates from the internal channel (fed by WebhookHandler).
	// It blocks until ctx is cancelled.
	slog.Info("telegram bot starting (webhook)", "channel", t.name)
	b.StartWebhook(ctx)
	slog.Info("telegram bot stopped", "channel", t.name)
}

func (t *Telegram) Send(ctx context.Context, text string) (MessageID, error) {
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

	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      sanitizeTelegramHTML(markdownToTelegramHTML(text)),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		return "", fmt.Errorf("telegram send: %w", err)
	}

	return MessageID(strconv.Itoa(msg.ID)), nil
}

func (t *Telegram) Edit(ctx context.Context, msgID MessageID, text string) error {
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

	telegramMsgID, err := strconv.Atoi(string(msgID))
	if err != nil {
		return fmt.Errorf("invalid telegram message id %q: %w", msgID, err)
	}

	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: telegramMsgID,
		Text:      sanitizeTelegramHTML(markdownToTelegramHTML(text)),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		return fmt.Errorf("telegram edit: %w", err)
	}

	return nil
}

func (t *Telegram) Done(_ context.Context) error {
	return nil
}

func (t *Telegram) SplitStatusMessages() bool {
	return true
}

func (t *Telegram) Markup() Markup {
	return MarkupHTML
}

func (t *Telegram) StatusWrap() StatusWrap {
	return StatusWrap{Open: "<blockquote expandable>", Close: "</blockquote>"}
}

// downloadMedia downloads the media attachment from a Telegram message to the
// configured MediaDir. Returns the absolute path so the agent can pass it
// directly to the Read tool without needing to resolve against a base dir.
func (t *Telegram) downloadMedia(ctx context.Context, b *bot.Bot, msg *models.Message) (string, error) {
	// Clean up old media files before downloading new ones.
	cleanupOldMedia(t.opts.MediaDir)

	fileID, ext := mediaFileInfo(msg)
	if fileID == "" {
		return "", fmt.Errorf("no supported media in message")
	}

	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
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

	// Limit the reader to prevent unexpectedly large downloads.
	body := io.LimitReader(resp.Body, maxMediaDownloadBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if len(data) > maxMediaDownloadBytes {
		return "", fmt.Errorf("file too large (downloaded %d bytes, max %d)", len(data), maxMediaDownloadBytes)
	}

	filename := mediaFilename(msg, ext)
	fullPath := filepath.Join(t.opts.MediaDir, filename)
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	// Return the absolute path so the agent can pass it directly to the Read
	// tool without needing to resolve it relative to any base directory.
	return fullPath, nil
}

// mediaFileInfo extracts the Telegram file ID and a file extension from the
// message's media attachment. Returns empty strings if no supported media.
func mediaFileInfo(msg *models.Message) (fileID string, ext string) {
	switch {
	case len(msg.Photo) > 0:
		// Telegram sends photos as an array of sizes — last is largest.
		largest := msg.Photo[len(msg.Photo)-1]
		return largest.FileID, ".jpg"
	case msg.Voice != nil:
		return msg.Voice.FileID, ".ogg"
	case msg.Audio != nil:
		ext := ".mp3"
		if msg.Audio.FileName != "" {
			if e := filepath.Ext(msg.Audio.FileName); e != "" {
				ext = e
			}
		}
		return msg.Audio.FileID, ext
	default:
		return "", ""
	}
}

// mediaFilename builds a unique filename for a downloaded media file.
func mediaFilename(msg *models.Message, ext string) string {
	ts := time.Now().Unix()
	prefix := "file"
	switch {
	case len(msg.Photo) > 0:
		prefix = "photo"
	case msg.Voice != nil:
		prefix = "voice"
	case msg.Audio != nil:
		prefix = "audio"
	}
	// Use the message ID as a simple collision-resistant suffix.
	return fmt.Sprintf("%s_%d_%d%s", prefix, ts, msg.ID, ext)
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
// attached media file so it knows to Read it.
func formatMediaMessage(text string, mediaPath string) string {
	mediaType := "file"
	ext := filepath.Ext(mediaPath)
	switch {
	case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp":
		mediaType = "image"
	case ext == ".ogg" || ext == ".mp3" || ext == ".m4a" || ext == ".wav" || ext == ".flac":
		mediaType = "audio"
	}

	attachment := fmt.Sprintf("[Attached %s: %s — view it with the Read tool]", mediaType, mediaPath)
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

// markdownBold matches **text** that the model emits despite being told to use HTML.
var markdownBold = regexp.MustCompile(`\*\*(.+?)\*\*`)

// markdownInlineCode matches `text` (single backtick, not triple).
var markdownInlineCode = regexp.MustCompile("(?s)`([^`]+)`")

// htmlTagRe matches HTML-like opening and closing tags.
var htmlTagRe = regexp.MustCompile(`<(/?[a-zA-Z][a-zA-Z0-9-]*)(\s[^>]*)?>`)

// supportedTelegramTags is the set of tag names that Telegram's HTML parser accepts.
var supportedTelegramTags = map[string]bool{
	"b": true, "i": true, "u": true, "s": true,
	"code": true, "pre": true, "a": true,
	"blockquote": true, "tg-spoiler": true,
}

// escapeUnsupportedTags replaces any HTML-like tag whose name is not in
// Telegram's supported set with its entity-escaped equivalent so Telegram
// doesn't reject the message with a parse error.
func escapeUnsupportedTags(s string) string {
	return htmlTagRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := htmlTagRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		// sub[1] is the tag name, with an optional leading "/" for closing tags.
		tagName := strings.ToLower(strings.TrimPrefix(sub[1], "/"))
		if supportedTelegramTags[tagName] {
			return match
		}
		return strings.ReplaceAll(strings.ReplaceAll(match, "<", "&lt;"), ">", "&gt;")
	})
}

// markdownToTelegramHTML converts common markdown patterns the model may
// produce into Telegram-compatible HTML. This is a best-effort fallback —
// the system prompt asks for HTML, but models sometimes slip into markdown.
// It also escapes any HTML-like tags that Telegram doesn't support so they
// don't cause parse errors.
func markdownToTelegramHTML(s string) string {
	// Only convert markdown if no HTML tags are present — if the model
	// followed the instructions we shouldn't double-convert.
	if !strings.Contains(s, "<b>") && !strings.Contains(s, "<code>") && !strings.Contains(s, "<pre>") {
		s = markdownBold.ReplaceAllString(s, "<b>$1</b>")
		s = markdownInlineCode.ReplaceAllString(s, "<code>$1</code>")
	}

	// Always escape unsupported tags — the model may include path-like strings
	// such as <userDir> that Telegram's HTML parser rejects.
	return escapeUnsupportedTags(s)
}

// truncateReplySnippet returns the first maxLen characters of s, appending "…"
// if truncated. Newlines are collapsed to spaces for a compact single-line preview.
func truncateReplySnippet(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= maxLen {
		return s
	}
	return string([]rune(s)[:maxLen]) + "…"
}
