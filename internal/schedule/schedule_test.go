package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/store"
	"tclaw/internal/schedule"
)

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

	require.NoError(t, s.Add(ctx, sched))

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "check emails", list[0].Prompt)
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

	require.NoError(t, s.Add(ctx, sched))

	t.Run("returns existing schedule", func(t *testing.T) {
		got, err := s.Get(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "hello", got.Prompt)
	})

	t.Run("non-existent returns nil", func(t *testing.T) {
		missing, err := s.Get(ctx, "sched_nonexistent")
		require.NoError(t, err)
		require.Nil(t, missing)
	})
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

	require.NoError(t, s.Add(ctx, sched))

	t.Run("updates existing schedule", func(t *testing.T) {
		err := s.Update(ctx, id, func(existing *schedule.Schedule) {
			existing.Prompt = "updated"
			existing.Status = schedule.StatusPaused
		})
		require.NoError(t, err)

		got, err := s.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "updated", got.Prompt)
		require.Equal(t, schedule.StatusPaused, got.Status)
	})
}

func TestStore_Update_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Update(ctx, "sched_nonexistent", func(existing *schedule.Schedule) {
		existing.Prompt = "nope"
	})
	require.Error(t, err)
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

	require.NoError(t, s.Add(ctx, sched))
	require.NoError(t, s.Remove(ctx, id))

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestStore_Remove_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Remove(ctx, "sched_nonexistent")
	require.Error(t, err)
}

func TestStore_ListEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Nil(t, list)
}

// --- helpers ---

func newTestStore(t *testing.T) *schedule.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return schedule.NewStore(s)
}
