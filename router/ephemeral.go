package router

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/libraries/secret"
	"tclaw/user"
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
	provisioners map[channel.ChannelType]channel.EphemeralProvisioner,
	onChannelChange func(),
) {
	ticker := time.NewTicker(ephemeralCheckInterval)
	defer ticker.Stop()

	lastLoggedError := make(map[string]string)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupOnce(ctx, userID, configWriter, runtimeState, tracker, secretStore, provisioners, onChannelChange, lastLoggedError)
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
	provisioners map[channel.ChannelType]channel.EphemeralProvisioner,
	onChannelChange func(),
	lastLoggedError map[string]string,
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

		if tracker != nil && tracker.IsBusyWithTimeout(ch.Name, timeout) {
			continue
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
			provisioner, ok := provisioners[ch.Type]
			if !ok {
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

		delete(lastLoggedError, ch.Name)
		cleaned = true
		slog.Info("ephemeral cleanup: channel torn down", "channel", ch.Name)
	}

	if cleaned && onChannelChange != nil {
		onChannelChange()
	}
}
