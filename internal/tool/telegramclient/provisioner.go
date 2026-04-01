package telegramclient

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/gotd/td/tg"

	"tclaw/internal/channel"
	"tclaw/internal/channel/telegramchannel"
	tgsdk "tclaw/internal/telegram"
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

	bf := tgsdk.NewBotFather(p.state.client)
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
			platformState = telegramchannel.NewPlatformState(userID)
		}
	}

	return &channel.ProvisionResult{
		Token:         result.Token,
		TeardownState: telegramchannel.NewTeardownState(result.Username),
		PlatformState: platformState,
	}, nil
}

// Teardown deletes a Telegram bot via BotFather using the stored teardown state.
func (p *Provisioner) Teardown(ctx context.Context, state channel.TeardownState) error {
	if state.Type != channel.PlatformTelegram {
		return fmt.Errorf("expected Telegram teardown state, got type %q", state.Type)
	}
	tgState, err := telegramchannel.ParseTeardownState(state)
	if err != nil {
		return fmt.Errorf("parse telegram teardown state: %w", err)
	}

	if err := ensureConnected(ctx, p.state); err != nil {
		return fmt.Errorf("connect to Telegram for bot deletion: %w", err)
	}

	p.state.botFatherMu.Lock()
	defer p.state.botFatherMu.Unlock()

	bf := tgsdk.NewBotFather(p.state.client)
	if err := bf.DeleteBot(ctx, tgState.BotUsername); err != nil {
		return fmt.Errorf("delete bot @%s via BotFather: %w", tgState.BotUsername, err)
	}

	return nil
}

// SendTeardownPrompt sends a confirmation prompt to the channel's Telegram chat.
func (p *Provisioner) SendTeardownPrompt(ctx context.Context, token string, platformState channel.PlatformState) error {
	if platformState.Type != channel.PlatformTelegram {
		return fmt.Errorf("expected Telegram platform state, got type %q", platformState.Type)
	}
	tgState, err := telegramchannel.ParsePlatformState(platformState)
	if err != nil {
		return fmt.Errorf("parse telegram platform state: %w", err)
	}
	if tgState.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send confirmation to user")
	}

	prompt := "⚠️ <b>This channel is about to be closed.</b>\n\nReply <b>yes</b> to confirm teardown, or anything else to cancel."
	if _, err := tgsdk.BotSend(token, tgState.ChatID, prompt); err != nil {
		return fmt.Errorf("send teardown prompt: %w", err)
	}
	return nil
}

// SendClosingMessage sends a brief acknowledgement after the user confirms
// channel teardown, before the bot is deleted.
func (p *Provisioner) SendClosingMessage(ctx context.Context, token string, platformState channel.PlatformState) error {
	if platformState.Type != channel.PlatformTelegram {
		return fmt.Errorf("expected Telegram platform state, got type %q", platformState.Type)
	}
	tgState, err := telegramchannel.ParsePlatformState(platformState)
	if err != nil {
		return fmt.Errorf("parse telegram platform state: %w", err)
	}
	if tgState.ChatID == 0 {
		return fmt.Errorf("no chat ID available — cannot send closing message")
	}

	msg := "✅ Confirmed — closing channel..."
	if _, err := tgsdk.BotSend(token, tgState.ChatID, msg); err != nil {
		return fmt.Errorf("send closing message: %w", err)
	}
	return nil
}

// ValidateCreate checks Telegram-specific constraints before provisioning.
func (p *Provisioner) ValidateCreate(description string) error {
	if p.TelegramUserID == "" {
		return fmt.Errorf("telegram.user_id is required in user config for Telegram channels")
	}
	if len([]rune(description)) > tgsdk.MaxBotPurposeRunes {
		return fmt.Errorf("description too long for Telegram channel: %d characters, max %d (used as bot display name)", len([]rune(description)), tgsdk.MaxBotPurposeRunes)
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
	if _, err := tgsdk.BotSend(token, userID, message); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	return nil
}

// PlatformResponseInfo returns Telegram-specific fields (bot username and link)
// for inclusion in tool responses.
func (p *Provisioner) PlatformResponseInfo(teardownState channel.TeardownState) map[string]any {
	if teardownState.Type != channel.PlatformTelegram {
		return nil
	}
	tgState, err := telegramchannel.ParseTeardownState(teardownState)
	if err != nil {
		return nil
	}
	if tgState.BotUsername == "" {
		return nil
	}
	return map[string]any{
		"platform_username": tgState.BotUsername,
		"platform_link":     "https://t.me/" + tgState.BotUsername,
	}
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
		RandomID: tgsdk.GenerateRandomID(),
	})
	if err != nil {
		return fmt.Errorf("send /start to @%s: %w", botUsername, err)
	}

	slog.Info("auto-started bot conversation", "bot", botUsername)
	return nil
}
