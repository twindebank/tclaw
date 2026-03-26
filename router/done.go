package router

import (
	"context"
	"log/slog"
	"strings"

	"tclaw/channel"
	"tclaw/libraries/secret"
)

// interceptPendingDone checks whether an inbound message is a response to a
// pending channel_done confirmation. When the agent calls channel_done on a
// Telegram channel, it sets PendingDone on the channel config and returns
// immediately (to avoid a deadlock where the tool blocks waiting for a reply
// that can't arrive until the blocking tool call returns).
//
// This function is called for every inbound message before it reaches the
// agent. If the channel has PendingDone set, the message is treated as the
// user's confirmation response:
//   - "yes" or "y": execute teardown and return true (message consumed)
//   - anything else: clear PendingDone and return false (message forwarded to agent)
//
// Returns false for all non-dynamic channels and channels without PendingDone.
func interceptPendingDone(
	ctx context.Context,
	msg channel.TaggedMessage,
	channelsFunc func() map[channel.ChannelID]channel.Channel,
	dynamicStore *channel.DynamicStore,
	secretStore secret.Store,
	provisioners map[channel.ChannelType]channel.EphemeralProvisioner,
	onChannelChange func(),
) bool {
	// Resolve channel ID to name. Only dynamic channels can have PendingDone.
	chMap := channelsFunc()
	if chMap == nil {
		return false
	}
	ch, ok := chMap[msg.ChannelID]
	if !ok {
		return false
	}
	chName := ch.Info().Name

	cfg, err := dynamicStore.Get(ctx, chName)
	if err != nil {
		slog.Error("interceptPendingDone: failed to read channel config", "channel", chName, "err", err)
		return false
	}
	if cfg == nil || !cfg.PendingDone {
		// Not a pending confirmation — pass through to agent.
		return false
	}

	text := strings.TrimSpace(strings.ToLower(msg.Text))
	if text != "yes" && text != "y" {
		// User cancelled — clear the flag and let the message reach the agent
		// so it can acknowledge the cancellation.
		if updateErr := dynamicStore.Update(ctx, chName, func(c *channel.DynamicChannelConfig) {
			c.PendingDone = false
		}); updateErr != nil {
			slog.Error("interceptPendingDone: failed to clear pending_done flag",
				"channel", chName, "err", updateErr)
		}
		slog.Info("interceptPendingDone: teardown cancelled by user", "channel", chName)
		return false
	}

	// User replied "yes" — execute teardown.
	slog.Info("interceptPendingDone: teardown confirmed by user, tearing down", "channel", chName)

	if cfg.TeardownState != nil {
		provisioner, hasProv := provisioners[cfg.Type]
		if !hasProv {
			slog.Error("interceptPendingDone: no provisioner for channel type, skipping platform teardown",
				"channel", chName, "type", cfg.Type)
		} else {
			if teardownErr := provisioner.Teardown(ctx, cfg.TeardownState); teardownErr != nil {
				// Do NOT delete the channel config — would orphan platform resources.
				slog.Error("interceptPendingDone: platform teardown failed, channel NOT deleted",
					"channel", chName, "err", teardownErr)
				return true
			}
		}
	}

	// Delete channel config.
	if removeErr := dynamicStore.Remove(ctx, chName); removeErr != nil {
		slog.Error("interceptPendingDone: failed to remove channel config",
			"channel", chName, "err", removeErr)
		return true
	}

	// Delete channel secret (best-effort).
	if deleteErr := secretStore.Delete(ctx, channel.ChannelSecretKey(chName)); deleteErr != nil {
		slog.Error("interceptPendingDone: failed to delete channel secret",
			"channel", chName, "err", deleteErr)
	}

	slog.Info("interceptPendingDone: channel torn down", "channel", chName)
	if onChannelChange != nil {
		onChannelChange()
	}
	return true
}
