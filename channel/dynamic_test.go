package channel_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
)

func TestDynamicStore_EmptyList(t *testing.T) {
	ds := channel.NewDynamicStore(newTestStore(t))
	configs, err := ds.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, configs)
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
	require.NoError(t, ds.Add(ctx, cfg))

	configs, err := ds.List(ctx)
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.Equal(t, "phone", configs[0].Name)
	require.Equal(t, "Mobile device", configs[0].Description)
}

func TestDynamicStore_AddDuplicate(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg := channel.DynamicChannelConfig{Name: "phone", Type: channel.TypeSocket}
	require.NoError(t, ds.Add(ctx, cfg))

	err := ds.Add(ctx, cfg)
	require.Error(t, err, "duplicate add should fail")
}

func TestDynamicStore_Get(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	t.Run("not found returns nil", func(t *testing.T) {
		got, err := ds.Get(ctx, "nonexistent")
		require.NoError(t, err)
		require.Nil(t, got)
	})

	cfg := channel.DynamicChannelConfig{Name: "tablet", Type: channel.TypeSocket, Description: "iPad"}
	require.NoError(t, ds.Add(ctx, cfg))

	t.Run("returns existing entry", func(t *testing.T) {
		got, err := ds.Get(ctx, "tablet")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "iPad", got.Description)
	})
}

func TestDynamicStore_Update(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg := channel.DynamicChannelConfig{Name: "phone", Type: channel.TypeSocket, Description: "Old phone"}
	require.NoError(t, ds.Add(ctx, cfg))

	t.Run("updates existing entry", func(t *testing.T) {
		err := ds.Update(ctx, "phone", func(c *channel.DynamicChannelConfig) {
			c.Description = "New phone"
		})
		require.NoError(t, err)

		got, err := ds.Get(ctx, "phone")
		require.NoError(t, err)
		require.Equal(t, "New phone", got.Description)
	})

	t.Run("nonexistent returns error", func(t *testing.T) {
		err := ds.Update(ctx, "nonexistent", func(c *channel.DynamicChannelConfig) {})
		require.Error(t, err)
	})
}

func TestDynamicStore_Remove(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	cfg1 := channel.DynamicChannelConfig{Name: "phone", Type: channel.TypeSocket}
	cfg2 := channel.DynamicChannelConfig{Name: "tablet", Type: channel.TypeSocket}
	require.NoError(t, ds.Add(ctx, cfg1))
	require.NoError(t, ds.Add(ctx, cfg2))

	t.Run("removes existing entry", func(t *testing.T) {
		require.NoError(t, ds.Remove(ctx, "phone"))

		configs, err := ds.List(ctx)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		require.Equal(t, "tablet", configs[0].Name)
	})

	t.Run("nonexistent returns error", func(t *testing.T) {
		err := ds.Remove(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestDynamicStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	ds := channel.NewDynamicStore(newTestStore(t))

	// Seed an initial entry.
	require.NoError(t, ds.Add(ctx, channel.DynamicChannelConfig{
		Name: "shared",
		Type: channel.TypeSocket,
	}))

	// Run concurrent updates — documents the TOCTOU race. Without a mutex
	// on the store, some updates may be lost. This test ensures no panics
	// or data corruption occur.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = ds.Update(ctx, "shared", func(c *channel.DynamicChannelConfig) {
				c.Description = time.Now().String()
			})
		}(i)
	}
	wg.Wait()

	// Verify the entry still exists and is readable.
	got, err := ds.Get(ctx, "shared")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "shared", got.Name)
}

// --- helpers ---

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return s
}
