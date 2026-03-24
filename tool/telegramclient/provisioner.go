package telegramclient

import (
	"context"
	"fmt"
	"log/slog"

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
