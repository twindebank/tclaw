package telegramclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

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

// SendTeardownPrompt sends a confirmation prompt to the channel's Telegram chat
// asking the user to reply "yes" to confirm teardown. Returns immediately after
// sending — the router intercepts the reply asynchronously via PendingDone.
func (p *Provisioner) SendTeardownPrompt(ctx context.Context, token string, platformState channel.PlatformState) error {
	tps, ok := platformState.(channel.TelegramPlatformState)
	if !ok {
		return fmt.Errorf("unexpected platform state type: %T (expected TelegramPlatformState)", platformState)
	}
	if tps.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send confirmation to user")
	}

	prompt := "⚠️ <b>This channel is about to be closed.</b>\n\nReply <b>yes</b> to confirm teardown, or anything else to cancel."
	if _, err := telegramBotSend(token, tps.ChatID, prompt); err != nil {
		return fmt.Errorf("send teardown prompt: %w", err)
	}
	return nil
}

// SendClosingMessage sends a brief acknowledgement after the user confirms channel
// teardown, before the bot is deleted. Best-effort — the caller should log but
// not abort teardown if this fails.
func (p *Provisioner) SendClosingMessage(ctx context.Context, token string, platformState channel.PlatformState) error {
	tps, ok := platformState.(channel.TelegramPlatformState)
	if !ok {
		return fmt.Errorf("unexpected platform state type: %T (expected TelegramPlatformState)", platformState)
	}
	if tps.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send closing message")
	}

	msg := "✅ Confirmed — closing channel..."
	if _, err := telegramBotSend(token, tps.ChatID, msg); err != nil {
		return fmt.Errorf("send closing message: %w", err)
	}
	return nil
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

// ValidateCreate checks Telegram-specific constraints before provisioning.
func (p *Provisioner) ValidateCreate(allowedUsers []int64, description string) error {
	if len(allowedUsers) == 0 {
		return fmt.Errorf("allowed_users is required for Telegram channels — at least one Telegram user ID must be specified (get your user ID from @userinfobot on Telegram)")
	}
	if len([]rune(description)) > MaxBotPurposeRunes {
		return fmt.Errorf("description too long for Telegram channel: %d characters, max %d (used as bot display name)", len([]rune(description)), MaxBotPurposeRunes)
	}
	return nil
}

// Notify sends an out-of-band message to allowed users via the Telegram Bot API.
// Returns the number of users successfully notified.
func (p *Provisioner) Notify(ctx context.Context, token string, allowedUsers []int64, message string) (int, error) {
	var sent int
	for _, userID := range allowedUsers {
		if _, err := telegramBotSend(token, userID, message); err != nil {
			slog.Warn("failed to notify user", "user_id", userID, "err", err)
			continue
		}
		sent++
	}
	if sent == 0 && len(allowedUsers) > 0 {
		return 0, fmt.Errorf("failed to notify any of %d users", len(allowedUsers))
	}
	return sent, nil
}

// PlatformResponseInfo returns Telegram-specific fields (bot username and link)
// for inclusion in tool responses.
func (p *Provisioner) PlatformResponseInfo(teardownState channel.TeardownState) map[string]any {
	ts, ok := teardownState.(channel.TelegramTeardownState)
	if !ok || ts.BotUsername == "" {
		return nil
	}
	return map[string]any{
		"platform_username": ts.BotUsername,
		"platform_link":     "https://t.me/" + ts.BotUsername,
	}
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
