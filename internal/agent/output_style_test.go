package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestResolveOutputStyleForChannel(t *testing.T) {
	t.Run("leaves the CLI default when nothing is set", func(t *testing.T) {
		require.Equal(t, "", resolveOutputStyleForChannel(Options{}, "ch1"))
	})

	t.Run("user-level style applies to every channel", func(t *testing.T) {
		opts := Options{OutputStyle: "Plain Words"}
		require.Equal(t, "Plain Words", resolveOutputStyleForChannel(opts, "ch1"))
		require.Equal(t, "Plain Words", resolveOutputStyleForChannel(opts, "ch2"))
	})

	t.Run("a channel's own style wins", func(t *testing.T) {
		opts := Options{
			OutputStyle:         "Plain Words",
			ChannelOutputStyles: map[channel.ChannelID]string{"ch1": "Explanatory"},
		}
		require.Equal(t, "Explanatory", resolveOutputStyleForChannel(opts, "ch1"))
		require.Equal(t, "Plain Words", resolveOutputStyleForChannel(opts, "ch2"))
	})

	t.Run("a channel can turn the style off", func(t *testing.T) {
		// Empty already means inherit, so opting out needs a word of its own.
		opts := Options{
			OutputStyle:         "Plain Words",
			ChannelOutputStyles: map[channel.ChannelID]string{"ch1": outputStyleOff},
		}
		require.Equal(t, "", resolveOutputStyleForChannel(opts, "ch1"))
		require.Equal(t, "Plain Words", resolveOutputStyleForChannel(opts, "ch2"))
	})
}

func TestBuildArgs_OutputStyle(t *testing.T) {
	t.Run("passes the style as settings JSON", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, OutputStyle: "Plain Words"})

		require.Contains(t, args, "--settings")
		require.Contains(t, args, `{"outputStyle":"Plain Words"}`)
	})

	t.Run("passes nothing when no style is set", func(t *testing.T) {
		// The CLI's own default must be left alone rather than overwritten with
		// an empty name, which it would not recognise.
		args := buildArgs(buildArgsParams{MaxTurns: 10})

		require.NotContains(t, args, "--settings")
	})

	t.Run("quotes a name containing a quote", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, OutputStyle: `Odd" name`})

		require.Contains(t, args, `{"outputStyle":"Odd\" name"}`)
	})
}
