package channel_test

import (
	"context"
	"testing"
	"time"

	"tclaw/channel"
	"tclaw/libraries/store"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return s
}

func TestDynamicStore_EmptyList(t *testing.T) {
	ds := channel.NewDynamicStore(newTestStore(t))
	configs, err := ds.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected empty list, got %d items", len(configs))
	}
}

func TestDynamicStore_AddAndList(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg := channel.DynamicChannelConfig{
		Name:        "phone",
		Type:        channel.TypeSocket,
		Description: "Mobile device",
		CreatedAt:   time.Now(),
	}
	if err := ds.Add(ctx, cfg); err != nil {
		t.Fatalf("add: %v", err)
	}

	configs, err := ds.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 item, got %d", len(configs))
	}
	if configs[0].Name != "phone" {
		t.Fatalf("expected name 'phone', got %q", configs[0].Name)
	}
	if configs[0].Description != "Mobile device" {
		t.Fatalf("expected description 'Mobile device', got %q", configs[0].Description)
	}
}

func TestDynamicStore_AddDuplicate(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg := channel.DynamicChannelConfig{Name: "phone", Type: channel.TypeSocket}
	if err := ds.Add(ctx, cfg); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := ds.Add(ctx, cfg); err == nil {
		t.Fatal("expected error on duplicate add, got nil")
	}
}

func TestDynamicStore_Get(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	// Not found returns nil.
	got, err := ds.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("get nonexistent: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for nonexistent, got %+v", got)
	}

	cfg := channel.DynamicChannelConfig{Name: "tablet", Type: channel.TypeSocket, Description: "iPad"}
	if err := ds.Add(ctx, cfg); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err = ds.Get(ctx, "tablet")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Description != "iPad" {
		t.Fatalf("expected 'iPad', got %q", got.Description)
	}
}

func TestDynamicStore_Update(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg := channel.DynamicChannelConfig{Name: "phone", Type: channel.TypeSocket, Description: "Old phone"}
	if err := ds.Add(ctx, cfg); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := ds.Update(ctx, "phone", func(c *channel.DynamicChannelConfig) {
		c.Description = "New phone"
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := ds.Get(ctx, "phone")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Description != "New phone" {
		t.Fatalf("expected 'New phone', got %q", got.Description)
	}

	// Update nonexistent returns error.
	if err := ds.Update(ctx, "nonexistent", func(c *channel.DynamicChannelConfig) {}); err == nil {
		t.Fatal("expected error on update nonexistent, got nil")
	}
}

func TestDynamicStore_Remove(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg1 := channel.DynamicChannelConfig{Name: "phone", Type: channel.TypeSocket}
	cfg2 := channel.DynamicChannelConfig{Name: "tablet", Type: channel.TypeSocket}
	if err := ds.Add(ctx, cfg1); err != nil {
		t.Fatalf("add phone: %v", err)
	}
	if err := ds.Add(ctx, cfg2); err != nil {
		t.Fatalf("add tablet: %v", err)
	}

	if err := ds.Remove(ctx, "phone"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	configs, err := ds.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 item after remove, got %d", len(configs))
	}
	if configs[0].Name != "tablet" {
		t.Fatalf("expected 'tablet' to remain, got %q", configs[0].Name)
	}

	// Remove nonexistent returns error.
	if err := ds.Remove(ctx, "nonexistent"); err == nil {
		t.Fatal("expected error on remove nonexistent, got nil")
	}
}
