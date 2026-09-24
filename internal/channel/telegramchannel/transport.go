package telegramchannel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tclaw/internal/channel"
	tgsdk "tclaw/internal/telegram"
)

// maxMediaDownloadBytes is the maximum file size we'll download from Telegram.
// Conservative limit for the 512MB Fly VM.
const maxMediaDownloadBytes = 10 * 1024 * 1024

// mediaRetention is how long downloaded media files are kept before cleanup.
// Files older than this are deleted when new media is downloaded.
const mediaRetention = 24 * time.Hour

// allowedUpdates are the update types the bot handles. Named explicitly because a webhook keeps
// whatever list it was last registered with.
var allowedUpdates = []string{
	models.AllowedUpdateMessage,
	models.AllowedUpdateCallbackQuery,
	models.AllowedUpdateStoppedMessageGeneration,
}

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

	// richMessages holds the ids of recent messages sent as rich messages, oldest first in
	// richOrder, so an edit uses the same form the message was sent in.
	richMessages map[int]bool
	richOrder    []int
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
				if stopped := update.StoppedMessageGeneration; stopped != nil {
					t.handleGenerationStopped(ctx, stopped, out)
					return
				}
				if query := update.CallbackQuery; query != nil {
					t.handleCallbackQuery(handlerCtx, b, query, out)
					return
				}
				if update.Message == nil {
					return
				}
				msg := update.Message

				// Extract text — media messages use Caption, text messages use Text.
				text := normalizeCommand(msg.Text)
				if text == "" {
					text = msg.Caption
				}

				hasMedia := len(msg.Photo) > 0 || msg.Voice != nil || msg.Audio != nil || msg.Document != nil
				if text == "" && !hasMedia {
					return
				}

				// Reject messages from users not in the allowlist.
				fromID := int64(0)
				if msg.From != nil {
					fromID = msg.From.ID
				}
				if !t.userAllowed(fromID) {
					slog.Warn("telegram message from unauthorized user",
						"from_id", fromID,
						"channel", t.name,
					)
					return
				}

				chatID := msg.Chat.ID

				// Download media if present.
				if hasMedia && t.opts.MediaDir != "" {
					mediaPath, err := t.downloadMedia(handlerCtx, b, msg)
					if err != nil {
						slog.Error("failed to download media", "err", err, "channel", t.name)
						text = formatMediaError(text, err)
					} else {
						text = formatMediaMessage(text, mediaPath, msg)
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
			bot.WithAllowedUpdates(allowedUpdates),
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

		if err := registerCommands(ctx, b); err != nil {
			// The menu is a shortcut; typed keywords still work without it.
			slog.Warn("failed to register telegram command menu", "err", err, "channel", t.name)
		}

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
		AllowedUpdates: allowedUpdates,
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

	if opts.Rich {
		// A reply goes out as a rich message, which renders markdown natively:
		// tables, headings, code blocks and maths.
		return t.sendRich(ctx, b, chatID, text, silent)
	}

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

// sendRich sends text as a rich markdown message. If Telegram refuses it for anything but a rate
// limit, the reply goes out as plain text instead, so it still arrives.
func (t *Telegram) sendRich(ctx context.Context, b *bot.Bot, chatID int64, text string, silent bool) (channel.MessageID, error) {
	msg, err := b.SendRichMessage(ctx, &bot.SendRichMessageParams{
		ChatID:              chatID,
		RichMessage:         models.InputRichMessage{Markdown: withoutButtons(text)},
		DisableNotification: silent,
	})
	if err != nil && !fallBackToPlain(ctx, err) {
		return "", fmt.Errorf("telegram send rich message: %w", err)
	}
	if err != nil {
		slog.Warn("telegram send: rich message refused, falling back to plain text", "channel", t.name, "error", err)
		msg, err = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: t.plainFallback(text), DisableNotification: silent})
		if err != nil {
			return "", fmt.Errorf("telegram send plain fallback: %w", err)
		}
	}
	// A fallback is remembered too: a streamed reply often starts as markdown Telegram cannot
	// parse yet, and the next edit should try the rich form again.
	t.rememberRich(msg.ID)
	return channel.MessageID(strconv.Itoa(msg.ID)), nil
}

// editRich replaces a rich message's content, falling back to plain text as sendRich does.
func (t *Telegram) editRich(ctx context.Context, b *bot.Bot, chatID int64, msgID int, text string) error {
	_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   msgID,
		RichMessage: &models.InputRichMessage{Markdown: withoutButtons(text)},
	})
	if err == nil || isNotModifiedError(err) || !fallBackToPlain(ctx, err) {
		return err
	}

	slog.Warn("telegram edit: rich message refused, falling back to plain text", "channel", t.name, "error", err)
	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: t.plainFallback(text)})
	return err
}

// maxRichReplyLen leaves room under Telegram's 32,768-character rich message limit, counted
// here in bytes, which is never fewer than the characters.
const maxRichReplyLen = 30000

func (t *Telegram) MaxRichReplyLen() int {
	return maxRichReplyLen
}

// StreamDraft shows text as a draft the user watches being written, with a Stop button that
// arrives as the stop keyword. Telegram drops a draft 30 seconds after its last update.
func (t *Telegram) StreamDraft(ctx context.Context, p channel.StreamDraftParams) error {
	t.mu.Lock()
	chatID := t.currentChatID
	b := t.bot
	t.mu.Unlock()
	if chatID == 0 || b == nil {
		return fmt.Errorf("telegram draft: channel %q has no chat to show it in yet", t.name)
	}
	if _, err := b.SendRichMessageDraft(ctx, &bot.SendRichMessageDraftParams{
		ChatID:      chatID,
		DraftID:     int(p.DraftID),
		RichMessage: models.InputRichMessage{Markdown: withoutButtons(tgsdk.SanitizeUTF8(p.Text))},
		CanStop:     true,
	}); err != nil {
		return fmt.Errorf("telegram draft: %w", err)
	}
	return nil
}

// plainMessageLimit is the most a plain message may hold, in UTF-16 units. A rich reply can run
// to 32,768 characters, so its plain fallback can be far longer than this.
const plainMessageLimit = 4096

// plainFallback is text as a plain message: headed by the notice, and cut to fit.
func (t *Telegram) plainFallback(text string) string {
	fallback, cut := trimToUTF16Units(plainFallbackNotice+text, plainMessageLimit)
	if cut {
		slog.Warn("telegram plain fallback: reply cut to the plain message limit", "channel", t.name, "length", len(text))
	}
	return fallback
}

// plainFallbackNotice heads a reply that had to be sent without formatting.
const plainFallbackNotice = "⚠️ [formatting error — sent as plain text]\n\n"

// fallBackToPlain reports whether a refused rich message should be resent as plain text: any
// failure except a rate limit, which plain text would hit as well, or a cancelled request.
func fallBackToPlain(ctx context.Context, err error) bool {
	return ctx.Err() == nil && !bot.IsTooManyRequestsError(err) && !errors.Is(err, bot.ErrorTooManyRequests)
}

// buttonTag matches the opening or closing of a rich message button or button row.
var buttonTag = regexp.MustCompile(`(?i)<(/?)(tg-button)`)

// withoutButtons escapes any button tag in text the agent wrote, so it shows as text. A button
// the agent drew could carry any callback data and any label, so only tclaw's own prompts get buttons.
func withoutButtons(text string) string {
	return buttonTag.ReplaceAllString(text, "&lt;$1$2")
}

// richMessageMemory bounds how many sent rich messages the transport remembers. Only a reply
// still being streamed is ever edited, so the most recent few are enough.
const richMessageMemory = 256

// rememberRich records that a message was sent as a rich message, so edits to it keep that form.
func (t *Telegram) rememberRich(msgID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.richMessages == nil {
		t.richMessages = make(map[int]bool)
	}
	t.richMessages[msgID] = true
	t.richOrder = append(t.richOrder, msgID)
	if len(t.richOrder) > richMessageMemory {
		delete(t.richMessages, t.richOrder[0])
		t.richOrder = t.richOrder[1:]
	}
}

func (t *Telegram) isRich(msgID int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.richMessages[msgID]
}

// userAllowed reports whether a Telegram user may use this bot. An empty allowlist lets anyone in.
func (t *Telegram) userAllowed(userID int64) bool {
	if len(t.allowedUsers) == 0 {
		return true
	}
	_, ok := t.allowedUsers[userID]
	return ok
}

// handleGenerationStopped turns the Stop button on a streamed reply into the stop keyword, so it
// takes the same path as typing "stop". In a private chat the chat id is the user's id.
func (t *Telegram) handleGenerationStopped(ctx context.Context, stopped *models.MessageGenerationStopped, out chan<- string) {
	if !t.userAllowed(stopped.Chat.ID) {
		slog.Warn("telegram stop from unauthorized chat", "chat_id", stopped.Chat.ID, "channel", t.name)
		return
	}
	slog.Info("telegram stop button pressed", "channel", t.name, "draft_id", stopped.DraftID)
	select {
	case out <- stopKeyword:
	case <-ctx.Done():
	case <-time.After(30 * time.Second):
		slog.Warn("telegram stop dropped, pipeline blocked", "channel", t.name)
	}
}

// stopKeyword is the text the agent treats as a request to stop the turn in progress.
const stopKeyword = "stop"

// isNotModifiedError reports Telegram refusing an edit that changes nothing, which the caller treats as success.
func isNotModifiedError(err error) bool {
	return strings.Contains(err.Error(), "message is not modified")
}

// telegramCaptionLimit is what the Bot API accepts on a document, counted in
// UTF-16 units, well below the 4096 a plain message allows.
const telegramCaptionLimit = 1024

// trimToUTF16Units cuts text to the limit Telegram measures in, which counts a
// non-BMP character such as an emoji as two.
func trimToUTF16Units(text string, limit int) (string, bool) {
	if len(utf16.Encode([]rune(text))) <= limit {
		return text, false
	}
	used := 0
	for i, r := range text {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		// one unit held back for the ellipsis that marks the cut
		if used+width > limit-1 {
			return text[:i] + "…", true
		}
		used += width
	}
	return text, false
}

// SendFile uploads content as a Telegram document.
func (t *Telegram) SendFile(ctx context.Context, p channel.SendFileParams) (channel.MessageID, error) {
	t.mu.Lock()
	chatID := t.currentChatID
	b := t.bot
	t.mu.Unlock()

	if chatID == 0 {
		return "", fmt.Errorf("telegram send file: no chat ID set — channel %q has not received an inbound message yet", t.name)
	}
	if len(p.Content) == 0 {
		return "", fmt.Errorf("telegram send file: %q has no content", p.Filename)
	}

	if b == nil {
		// no inbound message yet, so Messages() has not built the bot
		var err error
		b, err = bot.New(t.token)
		if err != nil {
			return "", fmt.Errorf("telegram send file (create bot): %w", err)
		}
	}

	caption := p.Caption
	if !utf8.ValidString(caption) {
		slog.Warn("telegram send file: stripping invalid UTF-8 bytes from caption", "channel", t.name)
		caption = tgsdk.SanitizeUTF8(caption)
	}
	if trimmed, cut := trimToUTF16Units(caption, telegramCaptionLimit); cut {
		// a caption over the limit is a 400 the HTML fallback does not match,
		// and the document may have cost a credential to build
		slog.Warn("telegram send file: truncating caption", "channel", t.name)
		caption = trimmed
	}

	document := func() models.InputFile {
		return &models.InputFileUpload{Filename: p.Filename, Data: bytes.NewReader(p.Content)}
	}

	msg, err := b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:              chatID,
		Document:            document(),
		Caption:             tgsdk.SanitizeHTML(tgsdk.MarkdownToHTML(caption)),
		ParseMode:           models.ParseModeHTML,
		DisableNotification: !p.Opts.Notify,
	})
	if err != nil && isHTMLParseError(err) {
		// the document matters more than its caption's formatting, and the
		// caller may have spent a credential building it
		slog.Warn("telegram send file: HTML parse error in caption, falling back to plain text",
			"channel", t.name, "error", err)
		msg, err = b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:              chatID,
			Document:            document(),
			Caption:             tgsdk.StripAllTags(caption),
			DisableNotification: !p.Opts.Notify,
		})
	}
	if err != nil {
		return "", fmt.Errorf("telegram send file: %w", err)
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

	if t.isRich(telegramMsgID) {
		if err := t.editRich(ctx, b, chatID, telegramMsgID, text); err != nil {
			return fmt.Errorf("telegram edit rich message: %w", err)
		}
		return nil
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
	return channel.MarkupTelegram
}

func (t *Telegram) StatusWrap() channel.StatusWrap {
	return channel.StatusWrap{Open: "<blockquote expandable>", Close: "</blockquote>"}
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
	case msg.Document != nil:
		ext := filepath.Ext(msg.Document.FileName)
		if ext == "" {
			ext = documentExtFromMimeType(msg.Document.MimeType)
		}
		return msg.Document.FileID, ext
	default:
		return "", ""
	}
}

// documentExtFromMimeType maps common document MIME types to a file
// extension, used when Telegram doesn't supply one via the original filename.
func documentExtFromMimeType(mimeType string) string {
	switch mimeType {
	case "application/pdf":
		return ".pdf"
	case "application/msword":
		return ".doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	default:
		return ""
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
	case msg.Document != nil:
		prefix = "document"
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
// attached media file so it knows to Read it. For documents, it also notes
// the original filename and MIME type so the agent (and user) can tell what
// was actually sent, since the file on disk is renamed for collision-safety.
func formatMediaMessage(text string, mediaPath string, msg *models.Message) string {
	mediaType := "file"
	detail := ""
	ext := filepath.Ext(mediaPath)
	switch {
	case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp":
		mediaType = "image"
	case ext == ".ogg" || ext == ".mp3" || ext == ".m4a" || ext == ".wav" || ext == ".flac":
		mediaType = "audio"
	case msg.Document != nil:
		mediaType = "document"
		detail = documentDetail(msg.Document)
	}

	attachment := fmt.Sprintf("[Attached %s: %s%s — view it with the Read tool]", mediaType, mediaPath, detail)
	if text == "" {
		return attachment
	}
	return attachment + "\n" + text
}

// documentDetail formats the original filename and MIME type of a document
// attachment as a parenthetical suffix, e.g. " (original filename: notice.pdf,
// mime: application/pdf)". Omits fields Telegram didn't supply.
func documentDetail(doc *models.Document) string {
	switch {
	case doc.FileName != "" && doc.MimeType != "":
		return fmt.Sprintf(" (original filename: %s, mime: %s)", doc.FileName, doc.MimeType)
	case doc.FileName != "":
		return fmt.Sprintf(" (original filename: %s)", doc.FileName)
	case doc.MimeType != "":
		return fmt.Sprintf(" (mime: %s)", doc.MimeType)
	default:
		return ""
	}
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
