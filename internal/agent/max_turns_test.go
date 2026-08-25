package agent

import (
	"testing"

	"tclaw/internal/channel"

	"github.com/stretchr/testify/require"
)

func TestResolveMaxTurnsForChannel(t *testing.T) {
	t.Run("falls back to the built-in default when nothing is set", func(t *testing.T) {
		require.Equal(t, defaultMaxTurns, resolveMaxTurnsForChannel(Options{}, "ch1"))
	})

	t.Run("user-level limit applies to every channel", func(t *testing.T) {
		opts := Options{MaxTurns: 50}
		require.Equal(t, 50, resolveMaxTurnsForChannel(opts, "ch1"))
		require.Equal(t, 50, resolveMaxTurnsForChannel(opts, "ch2"))
	})

	t.Run("per-channel limit overrides the user-level limit", func(t *testing.T) {
		opts := Options{
			MaxTurns:        50,
			ChannelMaxTurns: map[channel.ChannelID]int{"email": 10},
		}
		require.Equal(t, 10, resolveMaxTurnsForChannel(opts, "email"), "email is capped by its own limit")
		require.Equal(t, 50, resolveMaxTurnsForChannel(opts, "dev"), "a channel without one keeps the user-level limit")
	})

	t.Run("a zero per-channel limit means inherit, not stop", func(t *testing.T) {
		opts := Options{
			MaxTurns:        50,
			ChannelMaxTurns: map[channel.ChannelID]int{"email": 0},
		}
		require.Equal(t, 50, resolveMaxTurnsForChannel(opts, "email"))
	})
}

func TestBuildArgs_MaxTurns(t *testing.T) {
	t.Run("passes the channel's limit to the CLI", func(t *testing.T) {
		args := buildArgs(buildArgsParams{
			Options:  Options{MaxTurns: 50},
			MaxTurns: 10,
			Prompt:   "hello",
		})

		require.Equal(t, "10", flagValue(t, args, "--max-turns"))
	})
}

// --- helpers ---

func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	require.Failf(t, "flag not found", "no %s in %v", flag, args)
	return ""
}
