package channel

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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

	mu            sync.Mutex
	currentChatID int64
	bot           *bot.Bot // set in Messages(), used by Send/Edit
}

func NewTelegram(token, name, description string, opts TelegramOptions) *Telegram {
	return &Telegram{
		token:       token,
		name:        name,
		description: description,
		source:      SourceStatic,
		opts:        opts,
	}
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

		b, err := bot.New(t.token,
			bot.WithDefaultHandler(func(_ context.Context, _ *bot.Bot, update *models.Update) {
				if update.Message == nil || update.Message.Text == "" {
					return
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
		)
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
	// Register the HTTP handler on the shared server.
	webhookPath := "/telegram/" + t.name
	t.opts.RegisterHandler(webhookPath, b.WebhookHandler())

	// Tell Telegram to send updates to our webhook URL.
	ok, err := b.SetWebhook(ctx, &bot.SetWebhookParams{
		URL:                t.opts.WebhookURL,
		DropPendingUpdates: true,
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
		Text:      text,
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
		Text:      text,
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
