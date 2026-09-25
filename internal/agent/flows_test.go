package agent

import (
	"context"
	"errors"
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

	t.Run("the shared set follows each flow opening and closing", func(t *testing.T) {
		fm := NewFlowManager()
		fm.open = channel.NewOpenPrompts()

		fm.StartToolApproval("ch1", channel.TaggedMessage{ChannelID: "ch1"}, []string{"Bash"}, "s1")
		require.True(t, fm.open.IsOpen("ch1"))

		fm.Complete("ch1")
		require.False(t, fm.open.IsOpen("ch1"))

		fm.StartAuth("ch1", channel.TaggedMessage{ChannelID: "ch1"})
		require.True(t, fm.open.IsOpen("ch1"))

		fm.Cancel("ch1")
		require.False(t, fm.open.IsOpen("ch1"))
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
			channel.TaggedMessage{ChannelID: "ch1", Text: channel.ButtonPressText(channel.ButtonPress{PromptID: channel.NewPromptID(), Reply: channel.ReplyYes})},
			map[channel.ChannelID]string{})

		require.True(t, result.Handled)
		require.Nil(t, result.FallThroughMsg, "nothing is retried")
		require.NotNil(t, fm.Active("ch1"))
		require.Contains(t, ch.sends[len(ch.sends)-1], "out of date")
	})
}

func TestAnswersOpenApproval(t *testing.T) {
	fm := NewFlowManager()
	promptID := fm.StartToolApproval("ch1", channel.TaggedMessage{ChannelID: "ch1"}, []string{"Bash"}, "s1")

	t.Run("a press for the open approval is kept for it", func(t *testing.T) {
		require.True(t, answersOpenApproval(fm, "ch1", &channel.ButtonPress{PromptID: promptID, Reply: channel.ReplyYes}))
	})

	t.Run("an older prompt's press is not", func(t *testing.T) {
		require.False(t, answersOpenApproval(fm, "ch1", &channel.ButtonPress{PromptID: channel.NewPromptID(), Reply: channel.ReplyYes}))
	})

	t.Run("nor is a press on a channel with nothing open", func(t *testing.T) {
		require.False(t, answersOpenApproval(fm, "ch2", &channel.ButtonPress{PromptID: promptID, Reply: channel.ReplyYes}))
	})
}

func TestWouldReplaceOpenPrompt(t *testing.T) {
	scheduled := channel.TaggedMessage{ChannelID: "ch1", SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule}}
	typed := channel.TaggedMessage{ChannelID: "ch1", SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser}}
	denied := &ToolsDeniedError{Tools: []string{"Bash"}}

	withOpenApproval := func() *FlowManager {
		fm := NewFlowManager()
		fm.StartToolApproval("ch1", channel.TaggedMessage{ChannelID: "ch1"}, []string{"Write"}, "s1")
		return fm
	}

	tests := []struct {
		name string
		msg  channel.TaggedMessage
		err  error
		fm   *FlowManager
		want bool
	}{
		{name: "a scheduled turn denied a tool while an approval is open", msg: scheduled, err: denied, fm: withOpenApproval(), want: true},
		{name: "a scheduled turn needing sign-in while an approval is open", msg: scheduled, err: ErrAuthRequired, fm: withOpenApproval(), want: true},
		{name: "a scheduled turn with nothing open", msg: scheduled, err: denied, fm: NewFlowManager(), want: false},
		{name: "the user's own turn moves them on", msg: typed, err: denied, fm: withOpenApproval(), want: false},
		{name: "a turn that opens no prompt", msg: scheduled, err: errors.New("boom"), fm: withOpenApproval(), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, wouldReplaceOpenPrompt(tt.msg, tt.err, tt.fm))
		})
	}
}

func TestIsUserMessage(t *testing.T) {
	tests := []struct {
		name   string
		source *channel.MessageSourceInfo
		want   bool
	}{
		{name: "no source is the user's", source: nil, want: true},
		{name: "typed by the user", source: &channel.MessageSourceInfo{Source: channel.SourceUser}, want: true},
		{name: "sent from another channel", source: &channel.MessageSourceInfo{Source: channel.SourceChannel}, want: false},
		{name: "fired by a schedule", source: &channel.MessageSourceInfo{Source: channel.SourceSchedule}, want: false},
		{name: "a channel's creation brief", source: &channel.MessageSourceInfo{Source: channel.SourceInitialMessage}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUserMessage(channel.TaggedMessage{SourceInfo: tt.source}))
		})
	}
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
