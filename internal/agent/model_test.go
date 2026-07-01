package agent

import (
	"testing"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"

	"github.com/stretchr/testify/require"
)

func TestResolveModelForChannel(t *testing.T) {
	t.Run("falls back to user-level model when nothing else is set", func(t *testing.T) {
		opts := Options{Model: claudecli.ModelSonnet46}
		require.Equal(t, claudecli.ModelSonnet46, resolveModelForChannel(opts, "ch1"))
	})

	t.Run("per-channel model overrides the user-level model", func(t *testing.T) {
		opts := Options{
			Model: claudecli.ModelSonnet46,
			ChannelModels: map[channel.ChannelID]claudecli.Model{
				"ch1": claudecli.ModelOpus48,
			},
		}
		require.Equal(t, claudecli.ModelOpus48, resolveModelForChannel(opts, "ch1"))
		// A channel without an entry still gets the user-level model.
		require.Equal(t, claudecli.ModelSonnet46, resolveModelForChannel(opts, "ch2"))
	})

	t.Run("runtime override wins over per-channel and user-level models", func(t *testing.T) {
		opts := Options{
			Model:     claudecli.ModelSonnet46,
			ModelFunc: func() claudecli.Model { return claudecli.ModelOpus46 },
			ChannelModels: map[channel.ChannelID]claudecli.Model{
				"ch1": claudecli.ModelOpus48,
			},
		}
		require.Equal(t, claudecli.ModelOpus46, resolveModelForChannel(opts, "ch1"))
	})

	t.Run("empty runtime override falls through to per-channel model", func(t *testing.T) {
		opts := Options{
			Model:     claudecli.ModelSonnet46,
			ModelFunc: func() claudecli.Model { return claudecli.ModelAuto },
			ChannelModels: map[channel.ChannelID]claudecli.Model{
				"ch1": claudecli.ModelOpus48,
			},
		}
		require.Equal(t, claudecli.ModelOpus48, resolveModelForChannel(opts, "ch1"))
	})
}
