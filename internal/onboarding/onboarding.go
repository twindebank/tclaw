package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/libraries/store"
)

const storeKey = "onboarding"

// Phase tracks where the user is in the onboarding journey.
type Phase string

const (
	// PhaseWelcome is the initial state — the user has never interacted before.
	// The agent sends a welcome message and begins info gathering.
	PhaseWelcome Phase = "welcome"

	// PhaseInfoGathering is where the agent collects basic preferences
	// (name, home/work locations, timezone, etc.).
	PhaseInfoGathering Phase = "info_gathering"

	// PhaseTipsActive means the daily tips schedule is running.
	// The agent delivers feature tips progressively.
	PhaseTipsActive Phase = "tips_active"

	// PhaseComplete means all tips have been delivered and onboarding is done.
	PhaseComplete Phase = "complete"
)

// State represents the full onboarding state for a user.
type State struct {
	Phase     Phase     `json:"phase"`
	StartedAt time.Time `json:"started_at"`

	// InfoGathered tracks which pieces of info the user has provided.
	// Keys are info field names, values are true when collected.
	InfoGathered map[string]bool `json:"info_gathered,omitempty"`

	// TipsScheduleID is the ID of the auto-created tips schedule.
	// Empty if no schedule has been created yet.
	TipsScheduleID string `json:"tips_schedule_id,omitempty"`

	// TipsShown tracks which tip IDs have been delivered.
	TipsShown []string `json:"tips_shown,omitempty"`

	// CompletedAt is set when onboarding finishes.
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// Known info fields that onboarding collects.
const (
	InfoName = "name"
	InfoHome = "home_location"
	InfoWork = "work_location"
)

// AllInfoFields is the complete set of info the agent should try to gather.
var AllInfoFields = []string{InfoName, InfoHome, InfoWork}

// Store manages onboarding state persistence.
type Store struct {
	store store.Store
}

// NewStore creates an onboarding store backed by the given store.
func NewStore(s store.Store) *Store {
	return &Store{store: s}
}

// Get returns the current onboarding state, or nil if none exists.
func (s *Store) Get(ctx context.Context) (*State, error) {
	data, err := s.store.Get(ctx, storeKey)
	if err != nil {
		return nil, fmt.Errorf("read onboarding state: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse onboarding state: %w", err)
	}
	return &state, nil
}

// Set persists the onboarding state.
func (s *Store) Set(ctx context.Context, state *State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal onboarding state: %w", err)
	}
	if err := s.store.Set(ctx, storeKey, data); err != nil {
		return fmt.Errorf("save onboarding state: %w", err)
	}
	return nil
}

// Initialize creates the initial onboarding state if none exists.
// Returns the state (existing or newly created) and whether it was just created.
func (s *Store) Initialize(ctx context.Context) (*State, bool, error) {
	existing, err := s.Get(ctx)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}

	state := &State{
		Phase:        PhaseWelcome,
		StartedAt:    time.Now(),
		InfoGathered: make(map[string]bool),
	}
	if err := s.Set(ctx, state); err != nil {
		return nil, false, err
	}
	return state, true, nil
}
