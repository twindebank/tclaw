package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/channel"
	"tclaw/internal/mcp"
)

// Cross-channel messages should be concise summaries. This cap prevents
// abuse (e.g. prompt injection payloads) while leaving plenty of room
// for legitimate multi-paragraph messages.
const maxSendMessageLength = 8000

// SendDeps holds dependencies for the channel_send tool.
type SendDeps struct {
	// Links returns the current outbound link map (source channel name →
	// allowed targets). Called on each send so it picks up links from
	// all channels regardless of how they were created.
	Links func() map[string][]channel.Link

	// Output receives cross-channel messages for injection into the
	// target channel's message stream (same pattern as schedule injection).
	// Used only for normal (non no_reply) sends — no_reply sends bypass
	// the agent pipeline entirely via Send.
	Output chan<- channel.TaggedMessage

	// Channels resolves the current set of live channels. Called at send
	// time to map channel names to IDs.
	Channels func() map[channel.ChannelID]channel.Channel

	// ActiveChannel returns the name of the channel currently being
	// processed. Set by the router before each turn so the tool can
	// validate from_channel server-side — prevents prompt injection
	// from spoofing the source channel.
	ActiveChannel func() string

	// Send delivers text directly to a channel's transport (via the
	// outbox), bypassing the inbound agent pipeline entirely. Used for
	// no_reply sends: the target sees the message immediately but no CLI
	// turn is spawned, so no tokens are spent and nothing is added to the
	// target's own session history. If the user later replies to the
	// delivered message on Telegram, the transport's native reply-context
	// handling recovers it for a real follow-up turn.
	Send func(ctx context.Context, chID channel.ChannelID, text string, opts channel.SendOpts) (channel.MessageID, error)
}

// noReplyPrefix marks a directly-delivered message with its source channel,
// e.g. "📩 from email". Kept plain (no markup) to match other system-generated
// notices in this codebase (lifecycle notifications, cross-channel notices).
func noReplyPrefix(fromChannel string) string {
	return "📩 from " + fromChannel + "\n"
}

// RegisterSendTool adds the channel_send tool to the MCP handler.
// Separate from RegisterTools because it has different dependencies.
func RegisterSendTool(handler *mcp.Handler, deps SendDeps) {
	handler.Register(channelSendDef(), channelSendHandler(deps))
}

const ToolChannelSend = "channel_send"

func channelSendDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelSend,
		Description: "Send a message to another channel. The message arrives on the target channel " +
			"as if it were a new incoming message, waking the agent if idle. Only channels declared " +
			"as links in the config are valid targets. Use this when the current channel detects " +
			"something that requires action on another channel. Set no_reply to true to " +
			"report an already-actioned result: the message is delivered directly to the target " +
			"channel's transport (so the user sees it immediately) without waking the target agent or " +
			"spending any tokens — it is NOT added to the target's own conversation session, though the " +
			"user can reply to it there to pull it into a real follow-up turn.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"from_channel": {
					"type": "string",
					"description": "The name of the channel sending this message (your current channel from the Message Context)."
				},
				"to_channel": {
					"type": "string",
					"description": "The name of the target channel to send the message to. Must be a declared link."
				},
				"message": {
					"type": "string",
					"description": "The message text to deliver to the target channel."
				},
				"no_reply": {
					"type": "boolean",
					"description": "Set to true for an informational, already-actioned update: delivered directly to the target channel's transport with no agent turn and no token cost — the target agent never processes it (the user can still reply to it there to start a real conversation). Defaults to false (normal cross-channel message that wakes the target agent to process).",
					"default": false
				}
			},
			"required": ["from_channel", "to_channel", "message"]
		}`),
	}
}

type channelSendParams struct {
	FromChannel string `json:"from_channel"`
	ToChannel   string `json:"to_channel"`
	Message     string `json:"message"`

	// NoReply marks the message as an informational, already-actioned update:
	// delivered directly to the target's transport, bypassing the agent
	// pipeline entirely. Defaults to false.
	NoReply bool `json:"no_reply"`
}

func channelSendHandler(deps SendDeps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p channelSendParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if p.FromChannel == "" {
			return nil, fmt.Errorf("from_channel is required")
		}
		if p.ToChannel == "" {
			return nil, fmt.Errorf("to_channel is required")
		}
		if p.Message == "" {
			return nil, fmt.Errorf("message is required")
		}
		if len(p.Message) > maxSendMessageLength {
			return nil, fmt.Errorf("message is too long (%d characters, max %d)", len(p.Message), maxSendMessageLength)
		}

		// Verify from_channel matches the actual active channel to prevent
		// prompt injection from spoofing the source.
		if active := deps.ActiveChannel(); active != p.FromChannel {
			return nil, fmt.Errorf("from_channel %q does not match active channel %q", p.FromChannel, active)
		}

		// Validate the outbound link exists.
		allLinks := deps.Links()
		links, ok := allLinks[p.FromChannel]
		if !ok {
			return nil, fmt.Errorf("channel %q has no outbound links configured", p.FromChannel)
		}
		linkFound := false
		for _, link := range links {
			if link.Target == p.ToChannel {
				linkFound = true
				break
			}
		}
		if !linkFound {
			return nil, fmt.Errorf("channel %q has no link to %q", p.FromChannel, p.ToChannel)
		}

		// Resolve the target channel name to an ID.
		channels := deps.Channels()
		if channels == nil {
			return nil, fmt.Errorf("no channels available")
		}
		var targetID channel.ChannelID
		found := false
		for _, ch := range channels {
			if ch.Info().Name == p.ToChannel {
				targetID = ch.Info().ID
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("target channel %q not found in active channels", p.ToChannel)
		}

		// no_reply bypasses the agent pipeline entirely: deliver straight to the
		// target's transport so it's never queued, never spawns a CLI turn, and
		// never lands in the target's own session history.
		if p.NoReply {
			if _, err := deps.Send(ctx, targetID, noReplyPrefix(p.FromChannel)+p.Message, channel.SendOpts{Notify: true}); err != nil {
				return nil, fmt.Errorf("deliver no-reply message: %w", err)
			}
			return json.Marshal(map[string]any{
				"status":   "delivered",
				"from":     p.FromChannel,
				"to":       p.ToChannel,
				"message":  p.Message,
				"no_reply": true,
			})
		}

		msg := channel.TaggedMessage{
			ChannelID: targetID,
			Text:      p.Message,
			SourceInfo: &channel.MessageSourceInfo{
				Source:      channel.SourceChannel,
				FromChannel: p.FromChannel,
			},
		}

		select {
		case deps.Output <- msg:
			return json.Marshal(map[string]any{
				"status":   "sent",
				"from":     p.FromChannel,
				"to":       p.ToChannel,
				"message":  p.Message,
				"no_reply": false,
			})
		case <-ctx.Done():
			return nil, fmt.Errorf("send cancelled: %w", ctx.Err())
		}
	}
}
