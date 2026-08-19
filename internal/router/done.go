package router

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/repo"
	"tclaw/internal/user"
)

// confirmParams holds everything the confirmation handlers may need. Grouped
// because the set differs per action kind and threading them individually
// through the dispatcher would mean a parameter per kind.
type confirmParams struct {
	ChannelsFunc func() map[channel.ChannelID]channel.Channel
	RuntimeState *channel.RuntimeStateStore
	ConfigWriter *config.Writer
	UserID       user.ID
	SecretStore  secret.Store
	Provisioners channel.ProvisionerLookup
	RepoStore    *repo.Store
	Notify       func(ctx context.Context, chID channel.ChannelID, text string)

	OnChannelChange func()
	MemoryDir       string
}

// interceptPendingConfirmation checks whether an inbound message answers a
// confirmation the agent asked for. When a tool arms one it writes a
// PendingAction into the channel's runtime state and returns immediately,
// having sent the prompt straight to the chat rather than through the agent.
//
// This runs for every inbound message before it reaches the agent. With an
// action armed, the message is treated as the user's answer:
//   - "yes" or "y": perform the action and return true (message consumed)
//   - anything else: clear the action and return false (message forwarded on)
//
// Returning false on anything unexpected is deliberate: a confirmation that
// cannot be understood must fall through to the agent rather than be taken as
// consent.
func interceptPendingConfirmation(ctx context.Context, msg channel.TaggedMessage, params confirmParams) bool {
	chMap := params.ChannelsFunc()
	if chMap == nil {
		return false
	}
	ch, ok := chMap[msg.ChannelID]
	if !ok {
		return false
	}
	chName := ch.Info().Name

	rs, err := params.RuntimeState.Get(ctx, chName)
	if err != nil {
		slog.Error("pending confirmation: failed to read runtime state", "channel", chName, "err", err)
		return false
	}
	if rs.PendingAction == nil {
		return false
	}
	pending := *rs.PendingAction

	// Only a genuine user reply answers the confirmation. Automated traffic
	// (notifications, cross-channel sends, schedules) must never confirm,
	// cancel, or otherwise consume one — forward it untouched and leave the
	// action armed so the user's actual reply still lands. A nil SourceInfo is
	// treated as a user message (same convention as the agent loop).
	if msg.SourceInfo != nil && msg.SourceInfo.Source != channel.SourceUser {
		return false
	}

	if pending.Expired(time.Now()) {
		// A "yes" typed into a thread the user has lost track of must not act
		// on a prompt from hours ago.
		clearPendingAction(ctx, params.RuntimeState, chName)
		slog.Info("pending confirmation expired before it was answered",
			"channel", chName, "kind", pending.Kind)
		return false
	}

	text := strings.TrimSpace(strings.ToLower(msg.Text))
	if text != "yes" && text != "y" {
		// User declined — clear the action and forward to agent.
		clearPendingAction(ctx, params.RuntimeState, chName)
		slog.Info("pending confirmation declined by user", "channel", chName, "kind", pending.Kind)
		return false
	}

	clearPendingAction(ctx, params.RuntimeState, chName)

	switch pending.Kind {
	case channel.PendingChannelDone:
		return confirmChannelDone(ctx, ch, chName, rs, params)
	case channel.PendingRepoGrant:
		return confirmRepoGrant(ctx, msg.ChannelID, chName, pending, params)
	case channel.PendingRuleWrite:
		return confirmRuleWrite(ctx, msg.ChannelID, chName, pending, params)
	default:
		slog.Error("pending confirmation of unknown kind, ignoring",
			"channel", chName, "kind", pending.Kind)
		return false
	}
}

// clearPendingAction disarms the channel's confirmation. Failures are logged
// rather than returned: the caller has already decided what to do with the
// message, and a stuck flag is visible in the logs.
func clearPendingAction(ctx context.Context, runtimeState *channel.RuntimeStateStore, chName string) {
	if err := runtimeState.Update(ctx, chName, func(s *channel.RuntimeState) {
		s.PendingAction = nil
	}); err != nil {
		slog.Error("failed to clear pending confirmation", "channel", chName, "err", err)
	}
}

// confirmChannelDone tears the channel down after the user has confirmed.
func confirmChannelDone(
	ctx context.Context,
	ch channel.Channel,
	chName string,
	rs *channel.RuntimeState,
	params confirmParams,
) bool {
	configWriter := params.ConfigWriter
	userID := params.UserID
	secretStore := params.SecretStore
	provisioners := params.Provisioners
	onChannelChange := params.OnChannelChange
	memoryDir := params.MemoryDir
	runtimeState := params.RuntimeState

	slog.Info("channel_done: teardown confirmed", "channel", chName)

	// Send closing message before teardown (best-effort).
	if rs.PlatformState.HasPlatformState() {
		if provisioner := provisioners.Get(ch.Info().Type); provisioner != nil {
			token, tokenErr := secretStore.Get(ctx, channel.ChannelSecretKey(chName))
			if tokenErr != nil {
				slog.Warn("channel_done: failed to read token for closing message",
					"channel", chName, "err", tokenErr)
			} else if msgErr := provisioner.SendClosingMessage(ctx, token, rs.PlatformState); msgErr != nil {
				slog.Warn("channel_done: failed to send closing message",
					"channel", chName, "err", msgErr)
			}
		}
	}

	// Platform teardown.
	if rs.TeardownState.HasTeardownState() {
		provisioner := provisioners.Get(ch.Info().Type)
		if provisioner == nil {
			slog.Error("channel_done: no provisioner, skipping platform teardown",
				"channel", chName, "type", ch.Info().Type)
		} else {
			if teardownErr := provisioner.Teardown(ctx, rs.TeardownState); teardownErr != nil {
				slog.Error("channel_done: platform teardown failed, channel NOT deleted",
					"channel", chName, "err", teardownErr)
				return true
			}
		}
	}

	// Remove from config.
	if removeErr := configWriter.RemoveChannel(userID, chName); removeErr != nil {
		slog.Error("channel_done: failed to remove channel from config",
			"channel", chName, "err", removeErr)
		return true
	}

	// Clean up runtime state, secret, and knowledge dir (best-effort).
	if deleteErr := runtimeState.Delete(ctx, chName); deleteErr != nil {
		slog.Error("channel_done: failed to delete runtime state",
			"channel", chName, "err", deleteErr)
	}
	if deleteErr := secretStore.Delete(ctx, channel.ChannelSecretKey(chName)); deleteErr != nil {
		slog.Error("channel_done: failed to delete channel secret",
			"channel", chName, "err", deleteErr)
	}
	if memoryDir != "" {
		knowledgeDir := filepath.Join(memoryDir, "channels", chName)
		if removeErr := os.RemoveAll(knowledgeDir); removeErr != nil {
			slog.Warn("channel_done: failed to clean up channel knowledge dir",
				"channel", chName, "dir", knowledgeDir, "err", removeErr)
		}
	}

	slog.Info("channel_done: channel torn down", "channel", chName)
	if onChannelChange != nil {
		onChannelChange()
	}
	return true
}
