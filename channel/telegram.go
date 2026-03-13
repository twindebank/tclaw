package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// TelegramOptions configures optional webhook mode for a Telegram channel.
type TelegramOptions struct {
	// WebhookURL is the full URL Telegram should POST updates to.
	// If empty, the bot uses long polling instead.
	WebhookURL string

	// RegisterHandler adds an HTTP handler to the shared server.
	// Required when WebhookURL is set.
	RegisterHandler func(pattern string, handler http.Handler)
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
			bot.WithDefaultHandler(func(_ context.Context, _ *bot.Bot, update *models.Update) {
				if update.Message == nil || update.Message.Text == "" {
					return
				}

				// Reject messages from users not in the allowlist.
				if len(t.allowedUsers) > 0 {
					fromID := int64(0)
					if update.Message.From != nil {
						fromID = update.Message.From.ID
					}
					if _, ok := t.allowedUsers[fromID]; !ok {
						slog.Warn("telegram message from unauthorized user",
							"from_id", fromID,
							"channel", t.name,
						)
						return
					}
				}

				chatID := update.Message.Chat.ID
				text := update.Message.Text

				slog.Info("telegram message received",
					"chat_id", chatID,
					"length", len(text),
					"channel", t.name,
				)

				t.mu.Lock()
				t.currentChatID = chatID
				t.mu.Unlock()

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

		if len(t.allowedUsers) == 0 {
			slog.Warn("telegram bot has no allowed_users set — anyone who finds the bot username can message it",
				"channel", t.name,
			)
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
	slog.Info("telegram bot starting (polling)", "channel", t.name)
	b.Start(ctx)
	slog.Info("telegram bot stopped", "channel", t.name)
}

// startWebhook registers a webhook with Telegram and serves updates via HTTP.
func (t *Telegram) startWebhook(ctx context.Context, b *bot.Bot) {
	// Register the HTTP handler on the shared server.
	webhookPath := "/telegram/" + t.name
	t.opts.RegisterHandler(webhookPath, b.WebhookHandler())

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

	if b == nil || chatID == 0 {
		return "", nil
	}

	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      markdownToTelegramHTML(text),
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

	if b == nil || chatID == 0 {
		return nil
	}

	telegramMsgID, err := strconv.Atoi(string(msgID))
	if err != nil {
		return fmt.Errorf("invalid telegram message id %q: %w", msgID, err)
	}

	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: telegramMsgID,
		Text:      markdownToTelegramHTML(text),
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
	return StatusWrap{Open: "<tg-spoiler>", Close: "</tg-spoiler>"}
}

// markdownBold matches **text** that the model emits despite being told to use HTML.
var markdownBold = regexp.MustCompile(`\*\*(.+?)\*\*`)

// markdownInlineCode matches `text` (single backtick, not triple).
var markdownInlineCode = regexp.MustCompile("(?s)`([^`]+)`")

// markdownToTelegramHTML converts common markdown patterns the model may
// produce into Telegram-compatible HTML. This is a best-effort fallback —
// the system prompt asks for HTML, but models sometimes slip into markdown.
func markdownToTelegramHTML(s string) string {
	// Skip conversion if the text already contains HTML tags — the model
	// followed the instructions and we shouldn't double-convert.
	if strings.Contains(s, "<b>") || strings.Contains(s, "<code>") || strings.Contains(s, "<pre>") {
		return s
	}

	s = markdownBold.ReplaceAllString(s, "<b>$1</b>")
	s = markdownInlineCode.ReplaceAllString(s, "<code>$1</code>")
	return s
}
