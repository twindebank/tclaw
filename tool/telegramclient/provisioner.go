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
// It creates bots via BotFather and deletes them on teardown.
type Provisioner struct {
	state *handlerState

	// TelegramUserID is the user's Telegram user ID from top-level config.
	// Used for auto-start (/start the bot) and platform state (chat ID).
	TelegramUserID string
}

// IsReady returns true if the channel has a bot token in the secret store.
func (p *Provisioner) IsReady(ctx context.Context, channelName string) bool {
	token, err := p.state.deps.SecretStore.Get(ctx, channel.ChannelSecretKey(channelName))
	if err != nil {
		return false
	}
	return token != ""
}

// CanAutoProvision returns true if the Telegram Client API credentials are
// configured, allowing automatic bot creation via BotFather.
func (p *Provisioner) CanAutoProvision() bool {
	apiID, err := p.state.deps.SecretStore.Get(context.Background(), APIIDStoreKey)
	if err != nil || apiID == "" {
		return false
	}
	apiHash, err := p.state.deps.SecretStore.Get(context.Background(), APIHashStoreKey)
	if err != nil || apiHash == "" {
		return false
	}
	return true
}

// Provision creates a new Telegram bot via BotFather, auto-starts the
// conversation with the first allowed user (so the bot can message them),
// and returns the token and teardown state.
func (p *Provisioner) Provision(ctx context.Context, params channel.ProvisionParams) (*channel.ProvisionResult, error) {
	if err := ensureConnected(ctx, p.state); err != nil {
		return nil, fmt.Errorf("connect to Telegram: %w", err)
	}

	p.state.botFatherMu.Lock()
	defer p.state.botFatherMu.Unlock()

	bf := NewBotFather(p.state.client)
	result, err := bf.CreateBot(ctx, params.Purpose)
	if err != nil {
		return nil, fmt.Errorf("create bot via BotFather: %w", err)
	}

	// Store the token in the secret store so IsReady returns true.
	if err := p.state.deps.SecretStore.Set(ctx, channel.ChannelSecretKey(params.Name), result.Token); err != nil {
		return nil, fmt.Errorf("store bot token: %w", err)
	}

	// Auto-start: send /start as the user so the bot can message them back.
	var platformState channel.PlatformState
	if p.TelegramUserID != "" {
		userID, parseErr := strconv.ParseInt(p.TelegramUserID, 10, 64)
		if parseErr != nil {
			slog.Warn("invalid telegram user ID for auto-start", "user_id", p.TelegramUserID, "err", parseErr)
		} else {
			if startErr := p.StartBot(ctx, result.Username, userID); startErr != nil {
				slog.Warn("failed to auto-start bot (user will need to /start manually)", "bot", result.Username, "err", startErr)
			}
			// For Telegram DMs, chatID == userID.
			platformState = channel.NewTelegramPlatformState(userID)
		}
	}

	return &channel.ProvisionResult{
		Token:         result.Token,
		TeardownState: channel.NewTelegramTeardownState(result.Username),
		PlatformState: platformState,
	}, nil
}

// Teardown deletes a Telegram bot via BotFather using the stored teardown state.
func (p *Provisioner) Teardown(ctx context.Context, state channel.TeardownState) error {
	if state.Type != channel.PlatformTelegram || state.Telegram == nil {
		return fmt.Errorf("expected Telegram teardown state, got type %q", state.Type)
	}

	if err := ensureConnected(ctx, p.state); err != nil {
		return fmt.Errorf("connect to Telegram for bot deletion: %w", err)
	}

	p.state.botFatherMu.Lock()
	defer p.state.botFatherMu.Unlock()

	bf := NewBotFather(p.state.client)
	if err := bf.DeleteBot(ctx, state.Telegram.BotUsername); err != nil {
		return fmt.Errorf("delete bot @%s via BotFather: %w", state.Telegram.BotUsername, err)
	}

	return nil
}

// SendTeardownPrompt sends a confirmation prompt to the channel's Telegram chat.
func (p *Provisioner) SendTeardownPrompt(ctx context.Context, token string, platformState channel.PlatformState) error {
	if platformState.Type != channel.PlatformTelegram || platformState.Telegram == nil {
		return fmt.Errorf("expected Telegram platform state, got type %q", platformState.Type)
	}
	if platformState.Telegram.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send confirmation to user")
	}

	prompt := "⚠️ <b>This channel is about to be closed.</b>\n\nReply <b>yes</b> to confirm teardown, or anything else to cancel."
	if _, err := telegramBotSend(token, platformState.Telegram.ChatID, prompt); err != nil {
		return fmt.Errorf("send teardown prompt: %w", err)
	}
	return nil
}

// SendClosingMessage sends a brief acknowledgement after the user confirms
// channel teardown, before the bot is deleted.
func (p *Provisioner) SendClosingMessage(ctx context.Context, token string, platformState channel.PlatformState) error {
	if platformState.Type != channel.PlatformTelegram || platformState.Telegram == nil {
		return fmt.Errorf("expected Telegram platform state, got type %q", platformState.Type)
	}
	if platformState.Telegram.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send closing message")
	}

	msg := "✅ Confirmed — closing channel..."
	if _, err := telegramBotSend(token, platformState.Telegram.ChatID, msg); err != nil {
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
func (p *Provisioner) ValidateCreate(description string) error {
	if p.TelegramUserID == "" {
		return fmt.Errorf("telegram.user_id is required in user config for Telegram channels")
	}
	if len([]rune(description)) > MaxBotPurposeRunes {
		return fmt.Errorf("description too long for Telegram channel: %d characters, max %d (used as bot display name)", len([]rune(description)), MaxBotPurposeRunes)
	}
	return nil
}

// Notify sends a message to the configured Telegram user via the Bot API.
func (p *Provisioner) Notify(ctx context.Context, token string, message string) error {
	if p.TelegramUserID == "" {
		return fmt.Errorf("no telegram user ID configured")
	}
	userID, err := strconv.ParseInt(p.TelegramUserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram user ID %q: %w", p.TelegramUserID, err)
	}
	if _, err := telegramBotSend(token, userID, message); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	return nil
}

// PlatformResponseInfo returns Telegram-specific fields (bot username and link)
// for inclusion in tool responses.
func (p *Provisioner) PlatformResponseInfo(teardownState channel.TeardownState) map[string]any {
	if teardownState.Type != channel.PlatformTelegram || teardownState.Telegram == nil {
		return nil
	}
	if teardownState.Telegram.BotUsername == "" {
		return nil
	}
	return map[string]any{
		"platform_username": teardownState.Telegram.BotUsername,
		"platform_link":     "https://t.me/" + teardownState.Telegram.BotUsername,
	}
}

// ParseAllowedUserIDs converts string user IDs to int64 for Telegram API calls.
func ParseAllowedUserIDs(users []string) ([]int64, error) {
	ids := make([]int64, 0, len(users))
	for _, s := range users {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Telegram user ID %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// StartBot sends /start to a bot as the authenticated user via MTProto.
func (p *Provisioner) StartBot(ctx context.Context, botUsername string, userID int64) error {
	if err := ensureConnected(ctx, p.state); err != nil {
		return fmt.Errorf("connect to Telegram: %w", err)
	}

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
