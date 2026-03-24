package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/id"
	"tclaw/libraries/store"
)

const schedulesStoreKey = "schedules"

// ScheduleID uniquely identifies a schedule.
type ScheduleID string

// Status controls whether a schedule fires.
type Status string

const (
	StatusActive Status = "active"
	StatusPaused Status = "paused"
)

// Schedule defines a recurring prompt that fires on a cron expression.
type Schedule struct {
	ID          ScheduleID `json:"id"`
	CronExpr    string     `json:"cron_expr"`
	Prompt      string     `json:"prompt"`
	ChannelName string     `json:"channel_name"`
	Status      Status     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastRunAt   time.Time  `json:"last_run_at,omitempty"`
	NextRunAt   time.Time  `json:"next_run_at,omitempty"`

	// WaitForFree defers firing until the target channel is not busy.
	// When true, the scheduler checks ActivityTracker.IsBusy() before
	// injecting the prompt. If busy, the fire is skipped and retried
	// on the next scheduler tick (typically within seconds/minutes).
	WaitForFree bool `json:"wait_for_free,omitempty"`
}

// GenerateID creates a new unique schedule ID.
func GenerateID() ScheduleID {
	return ScheduleID(id.Generate("sched"))
}

// Store manages CRUD for scheduled prompts, stored as a JSON array
// under a single key (same pattern as channel.DynamicStore).
type Store struct {
	store store.Store
}

// NewStore creates a schedule store backed by the given store.
func NewStore(s store.Store) *Store {
	return &Store{store: s}
}

// List returns all schedules.
func (s *Store) List(ctx context.Context) ([]Schedule, error) {
	data, err := s.store.Get(ctx, schedulesStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read schedules: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var schedules []Schedule
	if err := json.Unmarshal(data, &schedules); err != nil {
		return nil, fmt.Errorf("parse schedules: %w", err)
	}
	return schedules, nil
}

// Get returns a single schedule by ID, or nil if not found.
func (s *Store) Get(ctx context.Context, scheduleID ScheduleID) (*Schedule, error) {
	schedules, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sched := range schedules {
		if sched.ID == scheduleID {
			return &sched, nil
		}
	}
	return nil, nil
}

// Add creates a new schedule. Returns an error if one with the same ID exists.
func (s *Store) Add(ctx context.Context, sched Schedule) error {
	schedules, err := s.List(ctx)
	if err != nil {
		return err
	}

	for _, existing := range schedules {
		if existing.ID == sched.ID {
			return fmt.Errorf("schedule %q already exists", sched.ID)
		}
	}

	schedules = append(schedules, sched)
	return s.save(ctx, schedules)
}

// Update applies updateFn to the schedule with the given ID.
func (s *Store) Update(ctx context.Context, scheduleID ScheduleID, updateFn func(*Schedule)) error {
	schedules, err := s.List(ctx)
	if err != nil {
		return err
	}

	found := false
	for i := range schedules {
		if schedules[i].ID == scheduleID {
			updateFn(&schedules[i])
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("schedule %q not found", scheduleID)
	}

	return s.save(ctx, schedules)
}

// Remove deletes a schedule by ID.
func (s *Store) Remove(ctx context.Context, scheduleID ScheduleID) error {
	schedules, err := s.List(ctx)
	if err != nil {
		return err
	}

	found := false
	var remaining []Schedule
	for _, sched := range schedules {
		if sched.ID == scheduleID {
			found = true
			continue
		}
		remaining = append(remaining, sched)
	}
	if !found {
		return fmt.Errorf("schedule %q not found", scheduleID)
	}

	return s.save(ctx, remaining)
}

func (s *Store) save(ctx context.Context, schedules []Schedule) error {
	data, err := json.Marshal(schedules)
	if err != nil {
		return fmt.Errorf("marshal schedules: %w", err)
	}
	if err := s.store.Set(ctx, schedulesStoreKey, data); err != nil {
		return fmt.Errorf("save schedules: %w", err)
	}
	return nil
}
