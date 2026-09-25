package claudecli

import (
	"errors"
	"fmt"
	"sort"
)

// Effort is how hard the model works on each turn, passed as --effort.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"

	// EffortUltracode is xhigh effort with the CLI free to orchestrate subagent workflows on its own.
	EffortUltracode Effort = "ultracode"
)

var validEfforts = map[Effort]bool{
	EffortLow: true, EffortMedium: true, EffortHigh: true, EffortXHigh: true, EffortMax: true, EffortUltracode: true,
}

// ValidEfforts returns the known effort levels, sorted.
func ValidEfforts() []Effort {
	out := make([]Effort, 0, len(validEfforts))
	for e := range validEfforts {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TurnSettings are CLI settings a user sets once and a channel may override. The zero value of
// each field means "not set here".
type TurnSettings struct {
	Effort Effort `yaml:"effort,omitempty" json:"effort,omitempty"`

	// MaxBudgetUSD stops a turn once its spend, subagents included, reaches this many dollars.
	MaxBudgetUSD float64 `yaml:"max_budget_usd,omitempty" json:"max_budget_usd,omitempty"`

	// FallbackModel is tried when the turn's model is overloaded or unavailable.
	FallbackModel Model `yaml:"fallback_model,omitempty" json:"fallback_model,omitempty"`
}

// Over returns s with every field it leaves unset taken from base.
func (s TurnSettings) Over(base TurnSettings) TurnSettings {
	out := base
	if s.Effort != "" {
		out.Effort = s.Effort
	}
	if s.MaxBudgetUSD != 0 {
		out.MaxBudgetUSD = s.MaxBudgetUSD
	}
	if s.FallbackModel != "" {
		out.FallbackModel = s.FallbackModel
	}
	return out
}

// Validate reports every field holding a value the CLI would reject.
func (s TurnSettings) Validate() error {
	var errs []error
	if s.Effort != "" && !validEfforts[s.Effort] {
		errs = append(errs, fmt.Errorf("unknown effort %q (known: %v)", s.Effort, ValidEfforts()))
	}
	if s.MaxBudgetUSD < 0 {
		errs = append(errs, fmt.Errorf("max_budget_usd must be zero (inherit) or positive, got %v", s.MaxBudgetUSD))
	}
	if s.FallbackModel != "" && !ValidModel(s.FallbackModel) {
		errs = append(errs, fmt.Errorf("unknown fallback_model %q", s.FallbackModel))
	}
	return errors.Join(errs...)
}
