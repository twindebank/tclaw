package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

// SendWhenFreeDeps holds dependencies for the channel_send_when_free tool.
type SendWhenFreeDeps struct {
	Links         func() map[string][]channel.Link
	Output        chan<- channel.TaggedMessage
	Channels      func() map[channel.ChannelID]channel.Channel
	ActiveChannel func() string
}

// RegisterSendWhenFreeTool adds the channel_send_when_free tool to the MCP handler.
func RegisterSendWhenFreeTool(handler *mcp.Handler, deps SendWhenFreeDeps) {
	handler.Register(channelSendWhenFreeDef(), channelSendWhenFreeHandler(deps))
}

const ToolChannelSendWhenFree = "channel_send_when_free"

func channelSendWhenFreeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelSendWhenFree,
		Description: "Send a message to another channel, delivering when the target is free. " +
			"The message enters the unified queue and is processed when the target channel " +
			"becomes idle. User messages on the target always take priority.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"from_channel": {
					"type": "string",
					"description": "The name of the channel sending this message (your current channel from the Message Context)."
				},
				"to_channel": {
					"type": "string",
					"description": "The name of the target channel. Must be a declared link."
				},
				"message": {
					"type": "string",
					"description": "The message text to deliver."
				}
			},
			"required": ["from_channel", "to_channel", "message"]
		}`),
	}
}

type channelSendWhenFreeParams struct {
	FromChannel string `json:"from_channel"`
	ToChannel   string `json:"to_channel"`
	Message     string `json:"message"`
}

func channelSendWhenFreeHandler(deps SendWhenFreeDeps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p channelSendWhenFreeParams
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

		if active := deps.ActiveChannel(); active != p.FromChannel {
			return nil, fmt.Errorf("from_channel %q does not match active channel %q", p.FromChannel, active)
		}

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

		// Deliver to the output channel. The unified queue handles busy-check
		// and priority — non-user messages wait for the target to be idle.
		if err := deliverMessage(deps.Output, deps.Channels, p.FromChannel, p.ToChannel, p.Message); err != nil {
			return nil, fmt.Errorf("deliver message: %w", err)
		}

		return json.Marshal(map[string]string{
			"status":  "queued",
			"from":    p.FromChannel,
			"to":      p.ToChannel,
			"message": fmt.Sprintf("Message to %q will be delivered when the channel is free.", p.ToChannel),
		})
	}
}

// deliverMessage injects a cross-channel message into the output channel.
func deliverMessage(output chan<- channel.TaggedMessage, channels func() map[channel.ChannelID]channel.Channel, from, to, message string) error {
	chMap := channels()
	if chMap == nil {
		return fmt.Errorf("no channels available")
	}

	var targetID channel.ChannelID
	found := false
	for _, ch := range chMap {
		if ch.Info().Name == to {
			targetID = ch.Info().ID
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("target channel %q not found in active channels", to)
	}

	msg := channel.TaggedMessage{
		ChannelID: targetID,
		Text:      message,
		SourceInfo: &channel.MessageSourceInfo{
			Source:      channel.SourceChannel,
			FromChannel: from,
		},
	}

	select {
	case output <- msg:
		return nil
	default:
		return fmt.Errorf("message buffer full for channel %q", to)
	}
}
