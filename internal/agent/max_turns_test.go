package agent

import (
	"testing"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"

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

func TestBuildArgs_TurnSettings(t *testing.T) {
	t.Run("passes each setting as its CLI flag", func(t *testing.T) {
		args := buildArgs(buildArgsParams{
			MaxTurns:     10,
			TurnSettings: claudecli.TurnSettings{Effort: claudecli.EffortLow, MaxBudgetUSD: 0.25, FallbackModel: claudecli.ModelSonnet5},
			Prompt:       "hello",
		})

		require.Equal(t, "low", flagValue(t, args, "--effort"))
		require.Equal(t, "0.25", flagValue(t, args, "--max-budget-usd"))
		require.Equal(t, "claude-sonnet-5", flagValue(t, args, "--fallback-model"))
	})

	t.Run("passes no flag for a setting left unset", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, Prompt: "hello"})

		require.NotContains(t, args, "--effort")
		require.NotContains(t, args, "--max-budget-usd")
		require.NotContains(t, args, "--fallback-model")
	})
}

func TestResolveTurnSettingsForChannel(t *testing.T) {
	t.Run("a channel's own settings win field by field over the user's", func(t *testing.T) {
		opts := Options{
			TurnSettings:        claudecli.TurnSettings{Effort: claudecli.EffortHigh, MaxBudgetUSD: 5},
			ChannelTurnSettings: map[channel.ChannelID]claudecli.TurnSettings{"triage": {Effort: claudecli.EffortLow}},
		}

		require.Equal(t, claudecli.TurnSettings{Effort: claudecli.EffortLow, MaxBudgetUSD: 5}, resolveTurnSettingsForChannel(opts, "triage"))
		require.Equal(t, claudecli.TurnSettings{Effort: claudecli.EffortHigh, MaxBudgetUSD: 5}, resolveTurnSettingsForChannel(opts, "dev"))
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
