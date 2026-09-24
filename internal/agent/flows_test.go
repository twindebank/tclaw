package agent

import (
	"context"
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

func TestHandleToolApprovalFlow(t *testing.T) {
	t.Run("a press on this prompt's yes button retries with the tools", func(t *testing.T) {
		fm, approval, ch, opts := approvalSetup(t)

		result := handleToolApprovalFlow(context.Background(), opts, fm, approval, ch,
			channel.TaggedMessage{ChannelID: "ch1", Text: channel.ButtonPressText(channel.ButtonPress{PromptID: approval.promptID, Reply: channel.ReplyYes})},
			map[channel.ChannelID]string{})

		require.NotNil(t, result.FallThroughMsg)
		require.Equal(t, "original", result.FallThroughMsg.Text, "the original message is retried")
		require.Nil(t, fm.Active("ch1"))
	})

	t.Run("a press on an older prompt's button leaves this one open", func(t *testing.T) {
		fm, approval, ch, opts := approvalSetup(t)

		result := handleToolApprovalFlow(context.Background(), opts, fm, approval, ch,
			channel.TaggedMessage{ChannelID: "ch1", Text: channel.ButtonPressText(channel.ButtonPress{PromptID: "older", Reply: channel.ReplyYes})},
			map[channel.ChannelID]string{})

		require.True(t, result.Handled)
		require.Nil(t, result.FallThroughMsg, "nothing is retried")
		require.NotNil(t, fm.Active("ch1"))
		require.Contains(t, ch.sends[len(ch.sends)-1], "out of date")
	})
}

func TestIsUserMessage(t *testing.T) {
	t.Run("only the user's own messages may answer a prompt", func(t *testing.T) {
		require.True(t, isUserMessage(channel.TaggedMessage{}))
		require.True(t, isUserMessage(channel.TaggedMessage{SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser}}))
		require.False(t, isUserMessage(channel.TaggedMessage{SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceChannel}}))
		require.False(t, isUserMessage(channel.TaggedMessage{SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule}}))
	})
}

// --- helpers ---

func approvalSetup(t *testing.T) (*FlowManager, *pendingToolApproval, *mockChannel, Options) {
	t.Helper()
	fm := NewFlowManager()
	fm.StartToolApproval("ch1", channel.TaggedMessage{ChannelID: "ch1", Text: "original"}, []string{"Bash"}, "sess-1")
	ch := &mockChannel{}
	opts := Options{Channels: map[channel.ChannelID]channel.Channel{"ch1": ch}}
	return fm, fm.Active("ch1").ToolApproval, ch, opts
}
