package telegramclient

import (
	"context"
	"fmt"

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

	bf := NewBotFather(p.state.client)
	if err := bf.DeleteBot(ctx, ts.BotUsername); err != nil {
		return fmt.Errorf("delete bot @%s via BotFather: %w", ts.BotUsername, err)
	}

	return nil
}
