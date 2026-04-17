package scheduletools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/schedule"
	"tclaw/internal/tool/scheduletools"
)

func setup(t *testing.T) (*mcp.Handler, *schedule.Store) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	schedStore := schedule.NewStore(s)
	output := make(chan channel.TaggedMessage, 8)
	scheduler := schedule.NewScheduler(schedule.SchedulerParams{
		Store:  schedStore,
		Output: output,
		Channels: func() map[channel.ChannelID]channel.Channel {
			return nil
		},
	})

	handler := mcp.NewHandler()
	scheduletools.RegisterTools(handler, scheduletools.Deps{
		Store:     schedStore,
		Scheduler: scheduler,
	})

	return handler, schedStore
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := h.Call(context.Background(), name, argsJSON)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = h.Call(context.Background(), name, argsJSON)
	if err == nil {
		t.Fatalf("expected error from %s, got nil", name)
	}
	return err
}

func TestScheduleCreate_AndList(t *testing.T) {
	h, _ := setup(t)

	// Create a schedule.
	result := callTool(t, h, "schedule_create", map[string]string{
		"prompt":       "check emails",
		"cron_expr":    "0 9 * * *",
		"channel_name": "desktop",
	})

	var createResult map[string]any
	if err := json.Unmarshal(result, &createResult); err != nil {
		t.Fatalf("unmarshal create result: %v", err)
	}
	if createResult["prompt"] != "check emails" {
		t.Fatalf("expected prompt 'check emails', got %v", createResult["prompt"])
	}
	if createResult["status"] != "active" {
		t.Fatalf("expected status 'active', got %v", createResult["status"])
	}

	// List should show it.
	listResult := callTool(t, h, "schedule_list", map[string]any{})

	var entries []struct {
		ID       string `json:"id"`
		Prompt   string `json:"prompt"`
		CronExpr string `json:"cron_expr"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(listResult, &entries); err != nil {
		t.Fatalf("unmarshal list result: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Prompt != "check emails" {
		t.Fatalf("expected 'check emails', got %q", entries[0].Prompt)
	}
}

func TestScheduleCreate_RejectsInvalidCron(t *testing.T) {
	h, _ := setup(t)

	callToolExpectError(t, h, "schedule_create", map[string]string{
		"prompt":       "test",
		"cron_expr":    "not a cron",
		"channel_name": "desktop",
	})
}

func TestScheduleCreate_RequiresChannelName(t *testing.T) {
	h, _ := setup(t)

	callToolExpectError(t, h, "schedule_create", map[string]string{
		"prompt":    "test",
		"cron_expr": "0 9 * * *",
	})
}

func TestScheduleEdit_UpdatesFields(t *testing.T) {
	h, schedStore := setup(t)

	// Create a schedule first.
	result := callTool(t, h, "schedule_create", map[string]string{
		"prompt":       "original",
		"cron_expr":    "0 9 * * *",
		"channel_name": "desktop",
	})

	var createResult map[string]any
	if err := json.Unmarshal(result, &createResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	schedID := createResult["id"].(string)

	// Edit the prompt and cron.
	callTool(t, h, "schedule_edit", map[string]string{
		"id":        schedID,
		"prompt":    "updated prompt",
		"cron_expr": "0 18 * * *",
	})

	// Verify in store.
	got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Prompt != "updated prompt" {
		t.Fatalf("expected 'updated prompt', got %q", got.Prompt)
	}
	if got.CronExpr != "0 18 * * *" {
		t.Fatalf("expected cron '0 18 * * *', got %q", got.CronExpr)
	}
}

func TestScheduleDelete_RemovesSchedule(t *testing.T) {
	h, schedStore := setup(t)

	result := callTool(t, h, "schedule_create", map[string]string{
		"prompt":       "to delete",
		"cron_expr":    "@daily",
		"channel_name": "desktop",
	})

	var createResult map[string]any
	if err := json.Unmarshal(result, &createResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	schedID := createResult["id"].(string)

	callTool(t, h, "schedule_delete", map[string]string{"id": schedID})

	got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSchedulePause_AndResume(t *testing.T) {
	h, schedStore := setup(t)

	result := callTool(t, h, "schedule_create", map[string]string{
		"prompt":       "pausable",
		"cron_expr":    "@hourly",
		"channel_name": "desktop",
	})

	var createResult map[string]any
	if err := json.Unmarshal(result, &createResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	schedID := createResult["id"].(string)

	// Pause.
	callTool(t, h, "schedule_pause", map[string]string{"id": schedID})

	got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != schedule.StatusPaused {
		t.Fatalf("expected paused, got %q", got.Status)
	}

	// Resume.
	callTool(t, h, "schedule_resume", map[string]string{"id": schedID})

	got, err = schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != schedule.StatusActive {
		t.Fatalf("expected active, got %q", got.Status)
	}
}

func TestScheduleList_Empty(t *testing.T) {
	h, _ := setup(t)

	result := callTool(t, h, "schedule_list", map[string]any{})

	var msg string
	if err := json.Unmarshal(result, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg != "No schedules configured." {
		t.Fatalf("expected empty message, got %q", msg)
	}
}

func TestScheduleCreate_AcceptsDescriptors(t *testing.T) {
	h, _ := setup(t)

	// @daily, @hourly, @weekly, @every should all work.
	for _, expr := range []string{"@daily", "@hourly", "@weekly", "@every 12h"} {
		callTool(t, h, "schedule_create", map[string]string{
			"prompt":       "test " + expr,
			"cron_expr":    expr,
			"channel_name": "desktop",
		})
	}
}

func TestScheduleCreate_WithTimezone(t *testing.T) {
	t.Run("stores timezone and shows in list", func(t *testing.T) {
		h, schedStore := setup(t)

		result := callTool(t, h, "schedule_create", map[string]string{
			"prompt":       "morning brief",
			"cron_expr":    "0 9 * * *",
			"channel_name": "desktop",
			"timezone":     "America/New_York",
		})

		var createResult map[string]any
		if err := json.Unmarshal(result, &createResult); err != nil {
			t.Fatalf("unmarshal create result: %v", err)
		}
		if createResult["timezone"] != "America/New_York" {
			t.Fatalf("expected timezone 'America/New_York' in result, got %v", createResult["timezone"])
		}

		schedID := createResult["id"].(string)
		got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
		if err != nil {
			t.Fatalf("get schedule: %v", err)
		}
		if got.Timezone != "America/New_York" {
			t.Fatalf("expected stored timezone 'America/New_York', got %q", got.Timezone)
		}
	})

	t.Run("timezone visible in schedule_list", func(t *testing.T) {
		h, _ := setup(t)

		callTool(t, h, "schedule_create", map[string]string{
			"prompt":       "check",
			"cron_expr":    "0 8 * * 1-5",
			"channel_name": "desktop",
			"timezone":     "Europe/London",
		})

		listResult := callTool(t, h, "schedule_list", map[string]any{})

		var entries []struct {
			Timezone string `json:"timezone"`
		}
		if err := json.Unmarshal(listResult, &entries); err != nil {
			t.Fatalf("unmarshal list result: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].Timezone != "Europe/London" {
			t.Fatalf("expected timezone 'Europe/London' in list, got %q", entries[0].Timezone)
		}
	})
}

func TestScheduleCreate_RejectsInvalidTimezone(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "schedule_create", map[string]string{
		"prompt":       "test",
		"cron_expr":    "0 9 * * *",
		"channel_name": "desktop",
		"timezone":     "Not/ATimezone",
	})
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestScheduleEdit_UpdatesTimezone(t *testing.T) {
	t.Run("sets new timezone and recalculates next_run_at", func(t *testing.T) {
		h, schedStore := setup(t)

		result := callTool(t, h, "schedule_create", map[string]string{
			"prompt":       "test",
			"cron_expr":    "0 9 * * *",
			"channel_name": "desktop",
		})
		var createResult map[string]any
		if err := json.Unmarshal(result, &createResult); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		schedID := createResult["id"].(string)

		callTool(t, h, "schedule_edit", map[string]any{
			"id":       schedID,
			"timezone": "Asia/Tokyo",
		})

		got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Timezone != "Asia/Tokyo" {
			t.Fatalf("expected timezone 'Asia/Tokyo', got %q", got.Timezone)
		}
		if got.NextRunAt.IsZero() {
			t.Fatal("expected NextRunAt to be set after timezone edit")
		}
	})

	t.Run("clears timezone when set to empty string", func(t *testing.T) {
		h, schedStore := setup(t)

		result := callTool(t, h, "schedule_create", map[string]string{
			"prompt":       "test",
			"cron_expr":    "0 9 * * *",
			"channel_name": "desktop",
			"timezone":     "Europe/London",
		})
		var createResult map[string]any
		if err := json.Unmarshal(result, &createResult); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		schedID := createResult["id"].(string)

		// Pass explicit empty string to reset to system default.
		callTool(t, h, "schedule_edit", map[string]any{
			"id":       schedID,
			"timezone": "",
		})

		got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Timezone != "" {
			t.Fatalf("expected empty timezone after reset, got %q", got.Timezone)
		}
	})
}

func TestScheduleCreate_OneShot(t *testing.T) {
	t.Run("creates one-shot with run_at", func(t *testing.T) {
		h, schedStore := setup(t)

		runAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		result := callTool(t, h, "schedule_create", map[string]any{
			"prompt":       "one-shot reminder",
			"run_at":       runAt,
			"channel_name": "desktop",
		})

		var createResult map[string]any
		if err := json.Unmarshal(result, &createResult); err != nil {
			t.Fatalf("unmarshal create result: %v", err)
		}
		if createResult["once"] != true {
			t.Fatalf("expected once=true, got %v", createResult["once"])
		}
		if createResult["prompt"] != "one-shot reminder" {
			t.Fatalf("expected prompt 'one-shot reminder', got %v", createResult["prompt"])
		}

		schedID := createResult["id"].(string)
		got, err := schedStore.Get(context.Background(), schedule.ScheduleID(schedID))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !got.Once {
			t.Fatal("expected Once=true in stored schedule")
		}
		if got.NextRunAt.IsZero() {
			t.Fatal("expected NextRunAt to be set")
		}
		if got.CronExpr != "" {
			t.Fatalf("expected empty CronExpr for one-shot, got %q", got.CronExpr)
		}
	})

	t.Run("rejects run_at in the past", func(t *testing.T) {
		h, _ := setup(t)

		pastTime := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
		callToolExpectError(t, h, "schedule_create", map[string]any{
			"prompt":       "past reminder",
			"run_at":       pastTime,
			"channel_name": "desktop",
		})
	})

	t.Run("rejects both run_at and cron_expr", func(t *testing.T) {
		h, _ := setup(t)

		callToolExpectError(t, h, "schedule_create", map[string]any{
			"prompt":       "conflicting",
			"run_at":       time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"cron_expr":    "0 9 * * *",
			"channel_name": "desktop",
		})
	})

	t.Run("rejects neither run_at nor cron_expr", func(t *testing.T) {
		h, _ := setup(t)

		callToolExpectError(t, h, "schedule_create", map[string]any{
			"prompt":       "missing schedule",
			"channel_name": "desktop",
		})
	})
}

func TestScheduleEdit_RejectsInvalidTimezone(t *testing.T) {
	h, _ := setup(t)

	result := callTool(t, h, "schedule_create", map[string]string{
		"prompt":       "test",
		"cron_expr":    "0 9 * * *",
		"channel_name": "desktop",
	})
	var createResult map[string]any
	if err := json.Unmarshal(result, &createResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	schedID := createResult["id"].(string)

	callToolExpectError(t, h, "schedule_edit", map[string]any{
		"id":       schedID,
		"timezone": "Fake/Timezone",
	})
}
