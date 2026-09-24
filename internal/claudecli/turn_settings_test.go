package claudecli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTurnSettings_Over(t *testing.T) {
	base := TurnSettings{Effort: EffortHigh, MaxBudgetUSD: 5, FallbackModel: ModelSonnet5}

	t.Run("an empty override keeps every base value", func(t *testing.T) {
		require.Equal(t, base, TurnSettings{}.Over(base))
	})

	t.Run("a set field wins and the rest inherit", func(t *testing.T) {
		got := TurnSettings{Effort: EffortLow}.Over(base)
		require.Equal(t, TurnSettings{Effort: EffortLow, MaxBudgetUSD: 5, FallbackModel: ModelSonnet5}, got)
	})
}

func TestTurnSettings_Validate(t *testing.T) {
	t.Run("accepts known values and unset fields", func(t *testing.T) {
		require.NoError(t, TurnSettings{}.Validate())
		require.NoError(t, TurnSettings{Effort: EffortUltracode, MaxBudgetUSD: 2.5, FallbackModel: ModelSonnet5}.Validate())
	})

	t.Run("reports every bad field together", func(t *testing.T) {
		err := TurnSettings{Effort: "extreme", MaxBudgetUSD: -1, FallbackModel: "claude-nonexistent"}.Validate()
		require.Error(t, err)
		require.Equal(t,
			`unknown effort "extreme" (known: [high low max medium ultracode xhigh])`+"\n"+
				"max_budget_usd must be zero (inherit) or positive, got -1\n"+
				`unknown fallback_model "claude-nonexistent"`,
			err.Error())
	})
}
