package telegramclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"tclaw/channel"
)

// Provisioner implements channel.EphemeralProvisioner for Telegram channels.
// It creates bots via BotFather and deletes them on teardown. Uses the same
// MTProto client and handlerState as the telegram_client_* MCP tools.
type Provisioner struct {
	state *handlerState
}

// Provision creates a new Telegram bot via BotFather and returns the token
// and teardown state for the channel.
func (p *Provisioner) Provision(ctx context.Context, name, purpose string) (*channel.ProvisionResult, error) {
	if err := ensureConnected(ctx, p.state); err != nil {
		return nil, fmt.Errorf("connect to Telegram: %w", err)
	}

	p.state.botFatherMu.Lock()
	defer p.state.botFatherMu.Unlock()

	bf := NewBotFather(p.state.client)
	result, err := bf.CreateBot(ctx, purpose)
	if err != nil {
		return nil, fmt.Errorf("create bot via BotFather: %w", err)
	}

	return &channel.ProvisionResult{
		Token: result.Token,
		TeardownState: channel.TelegramTeardownState{
			BotUsername: result.Username,
		},
	}, nil
}

// Teardown deletes a Telegram bot via BotFather using the stored teardown state.
func (p *Provisioner) Teardown(ctx context.Context, state channel.TeardownState) error {
	ts, ok := state.(channel.TelegramTeardownState)
	if !ok {
		return fmt.Errorf("unexpected teardown state type: %T (expected TelegramTeardownState)", state)
	}

	if err := ensureConnected(ctx, p.state); err != nil {
		return fmt.Errorf("connect to Telegram for bot deletion: %w", err)
	}

	p.state.botFatherMu.Lock()
	defer p.state.botFatherMu.Unlock()

	bf := NewBotFather(p.state.client)
	if err := bf.DeleteBot(ctx, ts.BotUsername); err != nil {
		return fmt.Errorf("delete bot @%s via BotFather: %w", ts.BotUsername, err)
	}

	return nil
}

// ConfirmTeardown sends a confirmation prompt to the channel's Telegram chat
// and polls for the user to reply "yes". Blocks until confirmation is received
// or the 60-second timeout expires.
func (p *Provisioner) ConfirmTeardown(ctx context.Context, token string, platformState channel.PlatformState) error {
	tps, ok := platformState.(channel.TelegramPlatformState)
	if !ok {
		return fmt.Errorf("unexpected platform state type: %T (expected TelegramPlatformState)", platformState)
	}
	if tps.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send confirmation to user")
	}

	// Send the confirmation prompt via the Bot HTTP API.
	prompt := "⚠️ <b>This channel is about to be closed.</b>\n\nReply <b>yes</b> to confirm, or anything else to cancel."
	msgID, err := telegramBotSend(token, tps.ChatID, prompt)
	if err != nil {
		return fmt.Errorf("send confirmation prompt: %w", err)
	}

	// Poll for the user's response via getUpdates. We use a short-lived
	// polling loop rather than webhooks because the channel's webhook handler
	// may not be running (it's about to be torn down).
	const pollTimeout = 60 * time.Second
	const pollInterval = 2 * time.Second

	deadline := time.Now().Add(pollTimeout)
	// Only look at updates after our confirmation message.
	updateOffset := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("teardown confirmation cancelled: %w", ctx.Err())
		default:
		}

		updates, pollErr := telegramBotGetUpdates(token, updateOffset, 5)
		if pollErr != nil {
			slog.Warn("confirm teardown: poll error, retrying", "err", pollErr)
			time.Sleep(pollInterval)
			continue
		}

		for _, update := range updates {
			updateOffset = update.UpdateID + 1

			if update.Message == nil {
				continue
			}
			// Only accept responses to our confirmation message.
			if update.Message.ReplyToMessage != nil && update.Message.ReplyToMessage.MessageID != msgID {
				continue
			}

			text := strings.TrimSpace(strings.ToLower(update.Message.Text))
			if text == "yes" || text == "y" {
				slog.Info("teardown confirmed by user", "chat_id", tps.ChatID)
				return nil
			}

			// Any non-yes response is a rejection.
			return fmt.Errorf("teardown rejected by user (replied %q)", update.Message.Text)
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("teardown confirmation timed out after %s — channel NOT closed", pollTimeout)
}

// telegramBotSend sends a message via the Telegram Bot HTTP API and returns
// the message ID.
func telegramBotSend(token string, chatID int64, text string) (int, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	resp, err := http.PostForm(apiURL, url.Values{
		"chat_id":    {strconv.FormatInt(chatID, 10)},
		"text":       {text},
		"parse_mode": {"HTML"},
	})
	if err != nil {
		return 0, fmt.Errorf("telegram sendMessage: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode sendMessage response: %w", err)
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram sendMessage failed: %s", result.Description)
	}
	return result.Result.MessageID, nil
}

// telegramBotGetUpdates polls for new messages via the Telegram Bot HTTP API.
func telegramBotGetUpdates(token string, offset, timeout int) ([]telegramUpdate, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", token)
	resp, err := http.PostForm(apiURL, url.Values{
		"offset":  {strconv.Itoa(offset)},
		"timeout": {strconv.Itoa(timeout)},
	})
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode getUpdates response: %w", err)
	}
	return result.Result, nil
}

type telegramUpdate struct {
	UpdateID int              `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID      int              `json:"message_id"`
	Text           string           `json:"text"`
	ReplyToMessage *telegramMessage `json:"reply_to_message"`
}

// StartBot sends /start to a bot as the authenticated user via MTProto.
// This initiates the conversation so the bot can message the user back
// (Telegram blocks bots from messaging users who haven't /started them).
func (p *Provisioner) StartBot(ctx context.Context, botUsername string, userID int64) error {
	if err := ensureConnected(ctx, p.state); err != nil {
		return fmt.Errorf("connect to Telegram: %w", err)
	}

	// Resolve the bot's username to a peer.
	resolved, err := p.state.client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: botUsername,
	})
	if err != nil {
		return fmt.Errorf("resolve bot @%s: %w", botUsername, err)
	}
	if len(resolved.Users) == 0 {
		return fmt.Errorf("bot @%s not found", botUsername)
	}

	u, ok := resolved.Users[0].(*tg.User)
	if !ok {
		return fmt.Errorf("unexpected user type for @%s", botUsername)
	}

	peer := &tg.InputPeerUser{
		UserID:     u.ID,
		AccessHash: u.AccessHash,
	}

	// Send /start to the bot as the user.
	_, err = p.state.client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  "/start",
		RandomID: generateRandomID(),
	})
	if err != nil {
		return fmt.Errorf("send /start to @%s: %w", botUsername, err)
	}

	slog.Info("auto-started bot conversation", "bot", botUsername)
	return nil
}
