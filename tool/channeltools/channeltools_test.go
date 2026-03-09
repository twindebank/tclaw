package channeltools_test

import (
	"context"
	"encoding/json"
	"testing"

	"tclaw/channel"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/tool/channeltools"
)

func setup(t *testing.T) (*mcp.Handler, *channel.DynamicStore) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	ds := channel.NewDynamicStore(s)
	handler := mcp.NewHandler()

	staticChannels := []channel.Info{
		{ID: "/tmp/test/desktop.sock", Type: channel.TypeSocket, Name: "desktop", Description: "Desktop workstation", Source: channel.SourceStatic},
	}

	channeltools.RegisterTools(handler, channeltools.Deps{
		DynamicStore:   ds,
		StaticChannels: staticChannels,
	})

	return handler, ds
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

func TestChannelList_ShowsStaticChannels(t *testing.T) {
	h, _ := setup(t)

	result := callTool(t, h, "channel_list", map[string]any{})

	var entries []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(result, &entries); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "desktop" {
		t.Fatalf("expected 'desktop', got %q", entries[0].Name)
	}
	if entries[0].Source != "static" {
		t.Fatalf("expected source 'static', got %q", entries[0].Source)
	}
}

func TestChannelCreate_AddsAndListsDynamic(t *testing.T) {
	h, _ := setup(t)

	// Create a dynamic channel.
	result := callTool(t, h, "channel_create", map[string]string{
		"name":        "phone",
		"description": "Mobile device",
	})

	var createResult map[string]any
	if err := json.Unmarshal(result, &createResult); err != nil {
		t.Fatalf("unmarshal create result: %v", err)
	}
	if createResult["name"] != "phone" {
		t.Fatalf("expected name 'phone', got %v", createResult["name"])
	}

	// List should show both static and dynamic.
	listResult := callTool(t, h, "channel_list", map[string]any{})

	var entries []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(listResult, &entries); err != nil {
		t.Fatalf("unmarshal list result: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Find the dynamic one.
	found := false
	for _, e := range entries {
		if e.Name == "phone" && e.Source == "dynamic" {
			found = true
		}
	}
	if !found {
		t.Fatal("dynamic channel 'phone' not found in list")
	}
}

func TestChannelCreate_RejectsStaticNameCollision(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "channel_create", map[string]string{
		"name":        "desktop",
		"description": "conflicts with static",
	})
	if err == nil {
		t.Fatal("expected error for static name collision")
	}
}

func TestChannelCreate_RejectsDuplicateDynamic(t *testing.T) {
	h, _ := setup(t)

	callTool(t, h, "channel_create", map[string]string{
		"name":        "phone",
		"description": "first",
	})

	err := callToolExpectError(t, h, "channel_create", map[string]string{
		"name":        "phone",
		"description": "duplicate",
	})
	if err == nil {
		t.Fatal("expected error for duplicate dynamic channel")
	}
}

func TestChannelEdit_UpdatesDynamic(t *testing.T) {
	h, ds := setup(t)

	callTool(t, h, "channel_create", map[string]string{
		"name":        "phone",
		"description": "Old description",
	})

	callTool(t, h, "channel_edit", map[string]string{
		"name":        "phone",
		"description": "New description",
	})

	// Verify the update was persisted.
	cfg, err := ds.Get(context.Background(), "phone")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.Description != "New description" {
		t.Fatalf("expected 'New description', got %q", cfg.Description)
	}
}

func TestChannelEdit_RejectsStatic(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "channel_edit", map[string]string{
		"name":        "desktop",
		"description": "try to edit static",
	})
	if err == nil {
		t.Fatal("expected error when editing static channel")
	}
}

func TestChannelDelete_RemovesDynamic(t *testing.T) {
	h, ds := setup(t)

	callTool(t, h, "channel_create", map[string]string{
		"name":        "phone",
		"description": "will be deleted",
	})

	callTool(t, h, "channel_delete", map[string]string{"name": "phone"})

	// Verify it's gone.
	cfg, err := ds.Get(context.Background(), "phone")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil after delete, channel still exists")
	}
}

func TestChannelDelete_RejectsStatic(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "channel_delete", map[string]string{"name": "desktop"})
	if err == nil {
		t.Fatal("expected error when deleting static channel")
	}
}

func TestChannelDelete_RejectsNonexistent(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "channel_delete", map[string]string{"name": "nonexistent"})
	if err == nil {
		t.Fatal("expected error when deleting nonexistent channel")
	}
}
