package router

import (
	"context"
	"log/slog"
	"time"

	"tclaw/channel"
	"tclaw/libraries/secret"
)

const (
	// ephemeralCheckInterval is how often the cleanup goroutine checks for
	// idle ephemeral channels.
	ephemeralCheckInterval = 60 * time.Second

	// defaultEphemeralIdleTimeout is the fallback if the channel config
	// has no explicit timeout set.
	defaultEphemeralIdleTimeout = 24 * time.Hour
)

// cleanupEphemeralChannels runs at user lifetime and periodically checks
// for ephemeral channels that have been idle past their timeout. When found,
// it tears down platform resources and deletes the channel.
//
// All state is read from the persistent DynamicStore on each tick, so this
// goroutine survives agent restarts with no in-memory state to lose.
func cleanupEphemeralChannels(
	ctx context.Context,
	dynamicStore *channel.DynamicStore,
	tracker *channel.ActivityTracker,
	secretStore secret.Store,
	provisioners map[channel.ChannelType]channel.EphemeralProvisioner,
	onChannelChange func(),
) {
	ticker := time.NewTicker(ephemeralCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupOnce(ctx, dynamicStore, tracker, secretStore, provisioners, onChannelChange)
		}
	}
}

func cleanupOnce(
	ctx context.Context,
	dynamicStore *channel.DynamicStore,
	tracker *channel.ActivityTracker,
	secretStore secret.Store,
	provisioners map[channel.ChannelType]channel.EphemeralProvisioner,
	onChannelChange func(),
) {
	configs, err := dynamicStore.List(ctx)
	if err != nil {
		slog.Error("ephemeral cleanup: failed to list dynamic channels", "err", err)
		return
	}

	cleaned := false
	for _, cfg := range configs {
		if !cfg.Ephemeral {
			continue
		}

		timeout := cfg.EphemeralIdleTimeout
		if timeout == 0 {
			timeout = defaultEphemeralIdleTimeout
		}

		// Check if the channel has been idle long enough.
		if tracker != nil && tracker.IsBusyWithTimeout(cfg.Name, timeout) {
			continue
		}

		slog.Info("ephemeral cleanup: channel idle past timeout, tearing down",
			"channel", cfg.Name, "timeout", timeout)

		// Platform-specific teardown.
		if cfg.TeardownState != nil {
			provisioner, ok := provisioners[cfg.Type]
			if !ok {
				slog.Error("ephemeral cleanup: no provisioner for channel type, skipping",
					"channel", cfg.Name, "type", cfg.Type)
				continue
			}
			if teardownErr := provisioner.Teardown(ctx, cfg.TeardownState); teardownErr != nil {
				// Don't delete the channel config — would orphan platform resources.
				slog.Error("ephemeral cleanup: platform teardown failed, will retry next tick",
					"channel", cfg.Name, "err", teardownErr)
				continue
			}
		}

		// Delete channel config.
		if removeErr := dynamicStore.Remove(ctx, cfg.Name); removeErr != nil {
			slog.Error("ephemeral cleanup: failed to remove channel config",
				"channel", cfg.Name, "err", removeErr)
			continue
		}

		// Delete channel secret (best-effort).
		if deleteErr := secretStore.Delete(ctx, channel.ChannelSecretKey(cfg.Name)); deleteErr != nil {
			slog.Error("ephemeral cleanup: failed to delete channel secret",
				"channel", cfg.Name, "err", deleteErr)
		}

		cleaned = true
		slog.Info("ephemeral cleanup: channel torn down", "channel", cfg.Name)
	}

	if cleaned && onChannelChange != nil {
		onChannelChange()
	}
}
