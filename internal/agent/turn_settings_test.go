package agent

import (
	"testing"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"

	"github.com/stretchr/testify/require"
)

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
