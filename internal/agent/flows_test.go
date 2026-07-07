package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestFlowManager(t *testing.T) {
	t.Run("starts auth flow", func(t *testing.T) {
		fm := NewFlowManager()
		msg := channel.TaggedMessage{ChannelID: "ch1", Text: "hello"}
		auth := fm.StartAuth("ch1", msg)

		require.NotNil(t, auth)
		require.Equal(t, authChoosing, auth.state)
		require.Equal(t, "hello", auth.originalMsg.Text)

		f := fm.Active("ch1")
		require.NotNil(t, f)
		require.Equal(t, FlowAuth, f.Kind)
	})

	t.Run("starts tool approval flow", func(t *testing.T) {
		fm := NewFlowManager()
		msg := channel.TaggedMessage{ChannelID: "ch1", Text: "original"}
		fm.StartToolApproval("ch1", msg, []string{"bash"}, "sess-123")

		f := fm.Active("ch1")
		require.NotNil(t, f)
		require.Equal(t, FlowToolApproval, f.Kind)
		require.Equal(t, "original", f.ToolApproval.originalMsg.Text)
		require.Equal(t, []string{"bash"}, f.ToolApproval.deniedTools)
	})

	t.Run("cancel removes flow", func(t *testing.T) {
		fm := NewFlowManager()
		fm.StartAuth("ch1", channel.TaggedMessage{})
		fm.Cancel("ch1")

		require.Nil(t, fm.Active("ch1"))
	})

	t.Run("complete removes flow", func(t *testing.T) {
		fm := NewFlowManager()
		fm.StartAuth("ch1", channel.TaggedMessage{})
		fm.Complete("ch1")

		require.Nil(t, fm.Active("ch1"))
	})

	t.Run("cancel on empty is no-op", func(t *testing.T) {
		fm := NewFlowManager()
		fm.Cancel("nonexistent")
		// No panic.
	})

	t.Run("start cancels existing flow", func(t *testing.T) {
		fm := NewFlowManager()

		// Start an auth flow.
		fm.StartAuth("ch1", channel.TaggedMessage{})
		require.Equal(t, FlowAuth, fm.Active("ch1").Kind)

		// Starting a tool approval flow on the same channel cancels auth.
		fm.StartToolApproval("ch1", channel.TaggedMessage{}, nil, "")
		require.Equal(t, FlowToolApproval, fm.Active("ch1").Kind)
	})

	t.Run("has flow", func(t *testing.T) {
		fm := NewFlowManager()

		require.False(t, fm.HasFlow("ch1", FlowAuth))

		fm.StartAuth("ch1", channel.TaggedMessage{})
		require.True(t, fm.HasFlow("ch1", FlowAuth))
		require.False(t, fm.HasFlow("ch1", FlowToolApproval))
	})

	t.Run("independent channels", func(t *testing.T) {
		fm := NewFlowManager()

		fm.StartAuth("ch1", channel.TaggedMessage{})
		fm.StartToolApproval("ch2", channel.TaggedMessage{}, nil, "")

		require.Equal(t, FlowAuth, fm.Active("ch1").Kind)
		require.Equal(t, FlowToolApproval, fm.Active("ch2").Kind)

		// Cancel ch1 doesn't affect ch2.
		fm.Cancel("ch1")
		require.Nil(t, fm.Active("ch1"))
		require.NotNil(t, fm.Active("ch2"))
	})
}
