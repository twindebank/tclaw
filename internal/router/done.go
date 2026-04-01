package router

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/user"
)

// interceptPendingDone checks whether an inbound message is a response to a
// pending channel_done confirmation. When the agent calls channel_done on a
// channel, it sets PendingDone in the runtime state and returns immediately.
//
// This function is called for every inbound message before it reaches the
// agent. If the channel has PendingDone set, the message is treated as the
// user's confirmation response:
//   - "yes" or "y": execute teardown and return true (message consumed)
//   - anything else: clear PendingDone and return false (message forwarded to agent)
func interceptPendingDone(
	ctx context.Context,
	msg channel.TaggedMessage,
	channelsFunc func() map[channel.ChannelID]channel.Channel,
	runtimeState *channel.RuntimeStateStore,
	configWriter *config.Writer,
	userID user.ID,
	secretStore secret.Store,
	provisioners channel.ProvisionerLookup,
	onChannelChange func(),
	memoryDir string,
) bool {
	chMap := channelsFunc()
	if chMap == nil {
		return false
	}
	ch, ok := chMap[msg.ChannelID]
	if !ok {
		return false
	}
	chName := ch.Info().Name

	rs, err := runtimeState.Get(ctx, chName)
	if err != nil {
		slog.Error("interceptPendingDone: failed to read runtime state", "channel", chName, "err", err)
		return false
	}
	if !rs.PendingDone {
		return false
	}

	text := strings.TrimSpace(strings.ToLower(msg.Text))
	if text != "yes" && text != "y" {
		// User cancelled — clear the flag and forward to agent.
		if updateErr := runtimeState.Update(ctx, chName, func(s *channel.RuntimeState) {
			s.PendingDone = false
		}); updateErr != nil {
			slog.Error("interceptPendingDone: failed to clear pending_done",
				"channel", chName, "err", updateErr)
		}
		slog.Info("interceptPendingDone: teardown cancelled by user", "channel", chName)
		return false
	}

	// User replied "yes" — execute teardown.
	slog.Info("interceptPendingDone: teardown confirmed", "channel", chName)

	// Send closing message before teardown (best-effort).
	if rs.PlatformState.HasPlatformState() {
		if provisioner := provisioners.Get(ch.Info().Type); provisioner != nil {
			token, tokenErr := secretStore.Get(ctx, channel.ChannelSecretKey(chName))
			if tokenErr != nil {
				slog.Warn("interceptPendingDone: failed to read token for closing message",
					"channel", chName, "err", tokenErr)
			} else if msgErr := provisioner.SendClosingMessage(ctx, token, rs.PlatformState); msgErr != nil {
				slog.Warn("interceptPendingDone: failed to send closing message",
					"channel", chName, "err", msgErr)
			}
		}
	}

	// Platform teardown.
	if rs.TeardownState.HasTeardownState() {
		provisioner := provisioners.Get(ch.Info().Type)
		if provisioner == nil {
			slog.Error("interceptPendingDone: no provisioner, skipping platform teardown",
				"channel", chName, "type", ch.Info().Type)
		} else {
			if teardownErr := provisioner.Teardown(ctx, rs.TeardownState); teardownErr != nil {
				slog.Error("interceptPendingDone: platform teardown failed, channel NOT deleted",
					"channel", chName, "err", teardownErr)
				return true
			}
		}
	}

	// Remove from config.
	if removeErr := configWriter.RemoveChannel(userID, chName); removeErr != nil {
		slog.Error("interceptPendingDone: failed to remove channel from config",
			"channel", chName, "err", removeErr)
		return true
	}

	// Clean up runtime state, secret, and knowledge dir (best-effort).
	if deleteErr := runtimeState.Delete(ctx, chName); deleteErr != nil {
		slog.Error("interceptPendingDone: failed to delete runtime state",
			"channel", chName, "err", deleteErr)
	}
	if deleteErr := secretStore.Delete(ctx, channel.ChannelSecretKey(chName)); deleteErr != nil {
		slog.Error("interceptPendingDone: failed to delete channel secret",
			"channel", chName, "err", deleteErr)
	}
	if memoryDir != "" {
		knowledgeDir := filepath.Join(memoryDir, "channels", chName)
		if removeErr := os.RemoveAll(knowledgeDir); removeErr != nil {
			slog.Warn("interceptPendingDone: failed to clean up channel knowledge dir",
				"channel", chName, "dir", knowledgeDir, "err", removeErr)
		}
	}

	slog.Info("interceptPendingDone: channel torn down", "channel", chName)
	if onChannelChange != nil {
		onChannelChange()
	}
	return true
}
