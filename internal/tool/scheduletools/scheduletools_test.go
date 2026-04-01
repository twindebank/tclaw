package scheduletools_test

import (
	"context"
	"encoding/json"
	"testing"

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
