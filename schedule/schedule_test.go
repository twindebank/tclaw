package schedule_test

import (
	"context"
	"testing"
	"time"

	"tclaw/libraries/store"
	"tclaw/schedule"
)

func newTestStore(t *testing.T) *schedule.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return schedule.NewStore(s)
}

func TestStore_AddAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sched := schedule.Schedule{
		ID:          schedule.GenerateID(),
		CronExpr:    "0 9 * * *",
		Prompt:      "check emails",
		ChannelName: "desktop",
		Status:      schedule.StatusActive,
		CreatedAt:   time.Now(),
	}

	if err := s.Add(ctx, sched); err != nil {
		t.Fatalf("add: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(list))
	}
	if list[0].Prompt != "check emails" {
		t.Fatalf("expected prompt 'check emails', got %q", list[0].Prompt)
	}
}

func TestStore_Get(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := schedule.GenerateID()
	sched := schedule.Schedule{
		ID:          id,
		CronExpr:    "@daily",
		Prompt:      "hello",
		ChannelName: "phone",
		Status:      schedule.StatusActive,
		CreatedAt:   time.Now(),
	}

	if err := s.Add(ctx, sched); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected schedule, got nil")
	}
	if got.Prompt != "hello" {
		t.Fatalf("expected prompt 'hello', got %q", got.Prompt)
	}

	// Non-existent ID.
	missing, err := s.Get(ctx, "sched_nonexistent")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Fatal("expected nil for nonexistent ID")
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := schedule.GenerateID()
	sched := schedule.Schedule{
		ID:          id,
		CronExpr:    "0 9 * * *",
		Prompt:      "original",
		ChannelName: "desktop",
		Status:      schedule.StatusActive,
		CreatedAt:   time.Now(),
	}

	if err := s.Add(ctx, sched); err != nil {
		t.Fatalf("add: %v", err)
	}

	err := s.Update(ctx, id, func(existing *schedule.Schedule) {
		existing.Prompt = "updated"
		existing.Status = schedule.StatusPaused
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Prompt != "updated" {
		t.Fatalf("expected prompt 'updated', got %q", got.Prompt)
	}
	if got.Status != schedule.StatusPaused {
		t.Fatalf("expected status paused, got %q", got.Status)
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Update(ctx, "sched_nonexistent", func(existing *schedule.Schedule) {
		existing.Prompt = "nope"
	})
	if err == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
}

func TestStore_Remove(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := schedule.GenerateID()
	sched := schedule.Schedule{
		ID:          id,
		CronExpr:    "@hourly",
		Prompt:      "to be deleted",
		ChannelName: "desktop",
		Status:      schedule.StatusActive,
		CreatedAt:   time.Now(),
	}

	if err := s.Add(ctx, sched); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := s.Remove(ctx, id); err != nil {
		t.Fatalf("remove: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 schedules after remove, got %d", len(list))
	}
}

func TestStore_Remove_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Remove(ctx, "sched_nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
}

func TestStore_ListEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list != nil {
		t.Fatalf("expected nil for empty list, got %v", list)
	}
}
