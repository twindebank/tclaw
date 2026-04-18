package router

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/dev"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/queue"
	"tclaw/internal/user"
)

const (
	ephemeralCheckInterval      = 60 * time.Second
	defaultEphemeralIdleTimeout = 24 * time.Hour
)

// cleanupEphemeralChannels runs at user lifetime and periodically checks
// for ephemeral channels that have been idle past their timeout. When found,
// it tears down platform resources and deletes the channel from config.
func cleanupEphemeralChannels(
	ctx context.Context,
	userID user.ID,
	configWriter *config.Writer,
	runtimeState *channel.RuntimeStateStore,
	tracker *channel.ActivityTracker,
	secretStore secret.Store,
	provisioners channel.ProvisionerLookup,
	onChannelChange func(),
	messageQueue *queue.Queue,
	channelsFunc func() map[channel.ChannelID]channel.Channel,
	devStore *dev.Store,
) {
	ticker := time.NewTicker(ephemeralCheckInterval)
	defer ticker.Stop()

	lastLoggedError := make(map[string]string)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupOnce(ctx, userID, configWriter, runtimeState, tracker, secretStore, provisioners, onChannelChange, lastLoggedError, messageQueue, channelsFunc, devStore)
		}
	}
}

func cleanupOnce(
	ctx context.Context,
	userID user.ID,
	configWriter *config.Writer,
	runtimeState *channel.RuntimeStateStore,
	tracker *channel.ActivityTracker,
	secretStore secret.Store,
	provisioners channel.ProvisionerLookup,
	onChannelChange func(),
	lastLoggedError map[string]string,
	messageQueue *queue.Queue,
	channelsFunc func() map[channel.ChannelID]channel.Channel,
	devStore *dev.Store,
) {
	channels, err := configWriter.ReadChannels(userID)
	if err != nil {
		slog.Error("ephemeral cleanup: failed to read channels", "err", err)
		return
	}

	cleaned := false
	for _, ch := range channels {
		if !ch.Ephemeral {
			continue
		}

		timeout := defaultEphemeralIdleTimeout
		if ch.EphemeralIdleTimeout != "" {
			if parsed, parseErr := time.ParseDuration(ch.EphemeralIdleTimeout); parseErr == nil {
				timeout = parsed
			}
		}

		if tracker != nil {
			busy, known := tracker.IsBusyWithTimeout(ch.Name, timeout)
			if !known {
				slog.Warn("ephemeral cleanup: no activity record for channel, skipping",
					"channel", ch.Name)
				continue
			}
			if busy {
				continue
			}
		}

		slog.Debug("ephemeral cleanup: channel idle past timeout",
			"channel", ch.Name, "timeout", timeout)

		// Platform-specific teardown via runtime state.
		rs, rsErr := runtimeState.Get(ctx, ch.Name)
		if rsErr != nil {
			slog.Error("ephemeral cleanup: failed to read runtime state", "channel", ch.Name, "err", rsErr)
			continue
		}

		if rs.TeardownState.HasTeardownState() {
			provisioner := provisioners.Get(ch.Type)
			if provisioner == nil {
				errMsg := fmt.Sprintf("no provisioner for channel type %s", ch.Type)
				if lastLoggedError[ch.Name] != errMsg {
					slog.Error("ephemeral cleanup: no provisioner, skipping",
						"channel", ch.Name, "type", ch.Type)
					lastLoggedError[ch.Name] = errMsg
				}
				continue
			}
			if teardownErr := provisioner.Teardown(ctx, rs.TeardownState); teardownErr != nil {
				errMsg := teardownErr.Error()
				if lastLoggedError[ch.Name] != errMsg {
					slog.Error("ephemeral cleanup: platform teardown failed, will retry",
						"channel", ch.Name, "err", teardownErr)
					lastLoggedError[ch.Name] = errMsg
				}
				continue
			}
		}

		// Remove from config.
		if removeErr := configWriter.RemoveChannel(userID, ch.Name); removeErr != nil {
			slog.Error("ephemeral cleanup: failed to remove from config",
				"channel", ch.Name, "err", removeErr)
			continue
		}

		// Clean up runtime state and secret (best-effort).
		if deleteErr := runtimeState.Delete(ctx, ch.Name); deleteErr != nil {
			slog.Error("ephemeral cleanup: failed to delete runtime state",
				"channel", ch.Name, "err", deleteErr)
		}
		if deleteErr := secretStore.Delete(ctx, channel.ChannelSecretKey(ch.Name)); deleteErr != nil {
			slog.Error("ephemeral cleanup: failed to delete channel secret",
				"channel", ch.Name, "err", deleteErr)
		}

		// Tear down any dev sessions bound to this channel. Best-effort —
		// failure here shouldn't prevent the channel from being removed.
		cleanupDevSessionsForChannel(ctx, devStore, ch.Name)

		delete(lastLoggedError, ch.Name)
		cleaned = true
		slog.Info("ephemeral cleanup: channel torn down", "channel", ch.Name)

		notifyParent(ctx, notifyParentParams{
			ChildName:    ch.Name,
			Parent:       ch.Parent,
			Message:      fmt.Sprintf("Ephemeral channel %q cleaned up (idle timeout).", ch.Name),
			Queue:        messageQueue,
			ChannelsFunc: channelsFunc,
		})
	}

	if cleaned && onChannelChange != nil {
		onChannelChange()
	}
}

// cleanupDevSessionsForChannel deletes every dev session bound to the given
// channel and removes its worktree directory on disk. Best-effort: errors are
// logged, never returned, since the channel itself has already been torn down
// by the time we get here.
func cleanupDevSessionsForChannel(ctx context.Context, devStore *dev.Store, channelName string) {
	if devStore == nil || channelName == "" {
		return
	}
	removed, err := devStore.DeleteSessionsByChannel(ctx, channelName)
	if err != nil {
		slog.Error("ephemeral cleanup: failed to delete dev sessions for channel",
			"channel", channelName, "err", err)
		return
	}
	for _, sess := range removed {
		slog.Info("ephemeral cleanup: removed dev session bound to channel",
			"channel", channelName, "branch", sess.Branch, "worktree", sess.WorktreeDir)
		// Remove the worktree directory. This leaves a stale entry in the
		// bare repo's worktree list (cleaned up by `git worktree prune` on
		// the next dev_start), but removes the bulk of on-disk state.
		if sess.WorktreeDir == "" {
			continue
		}
		if removeErr := os.RemoveAll(sess.WorktreeDir); removeErr != nil {
			slog.Error("ephemeral cleanup: failed to remove worktree dir",
				"channel", channelName, "branch", sess.Branch,
				"worktree", sess.WorktreeDir, "err", removeErr)
		}
	}
}
