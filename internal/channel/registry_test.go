package channel_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestRegistry(t *testing.T) {
	t.Run("Add makes name exist immediately", func(t *testing.T) {
		r := channel.NewRegistry(nil)
		require.False(t, r.NameExists("new-channel"))

		r.Add(channel.RegistryEntry{
			Info: channel.Info{Name: "new-channel"},
		})

		require.True(t, r.NameExists("new-channel"))
	})

	t.Run("Add entry appears in All", func(t *testing.T) {
		r := channel.NewRegistry([]channel.RegistryEntry{
			{Info: channel.Info{Name: "existing"}},
		})

		r.Add(channel.RegistryEntry{
			Info: channel.Info{Name: "added"},
		})

		all := r.All()
		require.Len(t, all, 2)

		names := make(map[string]bool)
		for _, e := range all {
			names[e.Name] = true
		}
		require.True(t, names["existing"])
		require.True(t, names["added"])
	})

	t.Run("Add then Remove restores original state", func(t *testing.T) {
		r := channel.NewRegistry(nil)

		r.Add(channel.RegistryEntry{
			Info: channel.Info{Name: "temp"},
		})
		require.True(t, r.NameExists("temp"))

		r.Remove("temp")
		require.False(t, r.NameExists("temp"))
		require.Empty(t, r.All())
	})

	t.Run("Reload replaces Add entries", func(t *testing.T) {
		r := channel.NewRegistry(nil)
		r.Add(channel.RegistryEntry{
			Info: channel.Info{Name: "added"},
		})

		r.Reload([]channel.RegistryEntry{
			{Info: channel.Info{Name: "reloaded"}},
		})

		require.False(t, r.NameExists("added"))
		require.True(t, r.NameExists("reloaded"))
	})
}
