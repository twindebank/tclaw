package router

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestAskPermission(t *testing.T) {
	t.Run("the user's yes press approves the call with its input unchanged", func(t *testing.T) {
		p, ch := permissionSetup(t)

		decision := answerPrompt(t, p, ch, func(prompt channel.SendPromptParams) channel.TaggedMessage {
			return userPress(prompt.PromptID, channel.ReplyYes)
		})

		require.Equal(t, permissionAllow, decision.Behavior)
		require.JSONEq(t, `{"command":"ls"}`, string(decision.UpdatedInput))
	})

	t.Run("a no press refuses the call", func(t *testing.T) {
		p, ch := permissionSetup(t)

		decision := answerPrompt(t, p, ch, func(prompt channel.SendPromptParams) channel.TaggedMessage {
			return userPress(prompt.PromptID, channel.ReplyNo)
		})

		require.Equal(t, permissionDeny, decision.Behavior)
	})

	t.Run("the prompt names the tool and what it would run", func(t *testing.T) {
		p, ch := permissionSetup(t)

		answerPrompt(t, p, ch, func(prompt channel.SendPromptParams) channel.TaggedMessage {
			require.Equal(t, "🔐 Allow **Bash**?\n\n`{\"command\":\"ls\"}`", prompt.Text)
			return userPress(prompt.PromptID, channel.ReplyNo)
		})
	})

	t.Run("refuses on a channel that cannot show buttons", func(t *testing.T) {
		p, _ := permissionSetup(t)
		plain := &stubDoneChannel{info: channel.Info{Name: "dev"}}
		p.Channels = func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{"dev-id": plain}
		}

		decision := askPermission(context.Background(), p, permissionRequest{ToolName: "Bash", Input: json.RawMessage(`{}`)})

		require.Equal(t, permissionDeny, decision.Behavior)
	})

	t.Run("refuses when the turn ends before an answer", func(t *testing.T) {
		p, _ := permissionSetup(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		decision := askPermission(ctx, p, permissionRequest{ToolName: "Bash", Input: json.RawMessage(`{}`)})

		require.Equal(t, permissionDeny, decision.Behavior)
	})
}

func TestPermissionPrompts_Resolve(t *testing.T) {
	t.Run("a press for no waiting prompt is not consumed", func(t *testing.T) {
		prompts := newPermissionPrompts()

		require.False(t, prompts.resolve(userPress(channel.NewPromptID(), channel.ReplyYes)))
	})

	t.Run("a press that did not come from the user answers nothing", func(t *testing.T) {
		prompts := newPermissionPrompts()
		promptID := channel.NewPromptID()
		reply := prompts.await(promptID)
		press := userPress(promptID, channel.ReplyYes)
		press.SourceInfo = &channel.MessageSourceInfo{Source: channel.SourceChannel}

		require.False(t, prompts.resolve(press))
		require.Empty(t, reply)
	})

	t.Run("ordinary text is not consumed", func(t *testing.T) {
		prompts := newPermissionPrompts()
		prompts.await(channel.NewPromptID())

		require.False(t, prompts.resolve(channel.TaggedMessage{Text: "yes"}), "a typed yes is left for the other flows")
	})
}

// --- helpers ---

func permissionSetup(t *testing.T) (permissionPromptParams, *promptChannel) {
	t.Helper()
	ch := &promptChannel{stubDoneChannel: stubDoneChannel{info: channel.Info{Name: "dev"}}, prompts: make(chan channel.SendPromptParams, 1)}
	return permissionPromptParams{
		Prompts:       newPermissionPrompts(),
		ActiveChannel: func() string { return "dev" },
		Channels: func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{"dev-id": ch}
		},
	}, ch
}

// answerPrompt asks for a Bash call and answers the prompt it produces with the message press builds.
func answerPrompt(t *testing.T, p permissionPromptParams, ch *promptChannel, press func(channel.SendPromptParams) channel.TaggedMessage) permissionDecision {
	t.Helper()
	decided := make(chan permissionDecision, 1)
	go func() {
		decided <- askPermission(context.Background(), p, permissionRequest{ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
	}()
	require.True(t, p.Prompts.resolve(press(<-ch.prompts)))
	return <-decided
}

func userPress(promptID string, reply channel.PromptReply) channel.TaggedMessage {
	return channel.TaggedMessage{
		ChannelID:  "dev-id",
		Text:       channel.ButtonPressText(channel.ButtonPress{PromptID: promptID, Reply: reply}),
		SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser},
	}
}

// promptChannel is a channel that can show buttons, and hands each prompt it is sent to the test.
type promptChannel struct {
	stubDoneChannel
	prompts chan channel.SendPromptParams
}

func (c *promptChannel) SendPrompt(_ context.Context, p channel.SendPromptParams) (channel.MessageID, error) {
	c.prompts <- p
	return "1", nil
}
