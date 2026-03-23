package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

// Cross-channel messages should be concise summaries. This cap prevents
// abuse (e.g. prompt injection payloads) while leaving plenty of room
// for legitimate multi-paragraph messages.
const maxSendMessageLength = 8000

// SendDeps holds dependencies for the channel_send tool.
type SendDeps struct {
	// Links returns the current outbound link map (source channel name →
	// allowed targets). Called on each send so it picks up links from
	// both static config and dynamic channels.
	Links func() map[string][]channel.Link

	// Output receives cross-channel messages for injection into the
	// target channel's message stream (same pattern as schedule injection).
	Output chan<- channel.TaggedMessage

	// Channels resolves the current set of live channels. Called at send
	// time to map channel names to IDs.
	Channels func() map[channel.ChannelID]channel.Channel

	// ActiveChannel returns the name of the channel currently being
	// processed. Set by the router before each turn so the tool can
	// validate from_channel server-side — prevents prompt injection
	// from spoofing the source channel.
	ActiveChannel func() string
}

// RegisterSendTool adds the channel_send tool to the MCP handler.
// Separate from RegisterTools because it has different dependencies.
func RegisterSendTool(handler *mcp.Handler, deps SendDeps) {
	handler.Register(channelSendDef(), channelSendHandler(deps))
}

func channelSendDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: "channel_send",
		Description: "Send a message to another channel. The message arrives on the target channel " +
			"as if it were a new incoming message, waking the agent if idle. Only channels declared " +
			"as links in the config are valid targets. Use this when the current channel detects " +
			"something that requires action on another channel.",
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
			return json.Marshal(map[string]string{
				"status":  "sent",
				"from":    p.FromChannel,
				"to":      p.ToChannel,
				"message": p.Message,
			})
		case <-ctx.Done():
			return nil, fmt.Errorf("send cancelled: %w", ctx.Err())
		}
	}
}
