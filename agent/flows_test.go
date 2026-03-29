package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
)

// --- FlowManager unit tests ---

func TestFlowManager_StartAndCancel(t *testing.T) {
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

	t.Run("starts reset flow", func(t *testing.T) {
		fm := NewFlowManager()
		reset := fm.StartReset("ch1")

		require.NotNil(t, reset)
		require.Equal(t, resetChoosing, reset.state)

		f := fm.Active("ch1")
		require.NotNil(t, f)
		require.Equal(t, FlowReset, f.Kind)
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
		fm.StartReset("ch1")
		fm.Cancel("ch1")

		require.Nil(t, fm.Active("ch1"))
	})

	t.Run("complete removes flow", func(t *testing.T) {
		fm := NewFlowManager()
		fm.StartReset("ch1")
		fm.Complete("ch1")

		require.Nil(t, fm.Active("ch1"))
	})

	t.Run("cancel on empty is no-op", func(t *testing.T) {
		fm := NewFlowManager()
		fm.Cancel("nonexistent")
		// No panic.
	})
}

func TestFlowManager_StartCancelsExisting(t *testing.T) {
	fm := NewFlowManager()

	// Start an auth flow.
	fm.StartAuth("ch1", channel.TaggedMessage{})
	require.Equal(t, FlowAuth, fm.Active("ch1").Kind)

	// Starting a reset flow on the same channel cancels auth.
	fm.StartReset("ch1")
	require.Equal(t, FlowReset, fm.Active("ch1").Kind)
}

func TestFlowManager_HasFlow(t *testing.T) {
	fm := NewFlowManager()

	require.False(t, fm.HasFlow("ch1", FlowAuth))

	fm.StartAuth("ch1", channel.TaggedMessage{})
	require.True(t, fm.HasFlow("ch1", FlowAuth))
	require.False(t, fm.HasFlow("ch1", FlowReset))
}

func TestFlowManager_IndependentChannels(t *testing.T) {
	fm := NewFlowManager()

	fm.StartAuth("ch1", channel.TaggedMessage{})
	fm.StartReset("ch2")

	require.Equal(t, FlowAuth, fm.Active("ch1").Kind)
	require.Equal(t, FlowReset, fm.Active("ch2").Kind)

	// Cancel ch1 doesn't affect ch2.
	fm.Cancel("ch1")
	require.Nil(t, fm.Active("ch1"))
	require.NotNil(t, fm.Active("ch2"))
}

// --- Integration tests using sendMessages ---

func TestFlowManager_ResetFlowViaAgent(t *testing.T) {
	t.Run("session reset via flow manager", func(t *testing.T) {
		var updatedSessionID string
		opts := Options{
			Sessions: map[channel.ChannelID]string{"test-ch": "old-session"},
			OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
				updatedSessionID = sessionID
			},
		}

		_, sends := sendMessages(t, opts, "reset", "1")

		// Session should be cleared.
		require.Equal(t, "", updatedSessionID)

		// Should have sent the menu then the confirmation.
		require.GreaterOrEqual(t, len(sends), 2)
		require.Contains(t, sends[len(sends)-1], "Session cleared")
	})

	t.Run("stop cancels active reset flow", func(t *testing.T) {
		// Send reset to start the flow, then stop to cancel it.
		_, sends := sendMessages(t, Options{}, "reset", "stop")

		// Menu should have been sent, but no reset confirmation.
		for _, s := range sends {
			require.NotContains(t, s, "Session cleared")
			require.NotContains(t, s, "reset complete")
		}
	})
}
