package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/channel"
	"tclaw/mcp"
)

const (
	defaultSendTimeout = 30 * time.Minute
	maxSendTimeout     = 2 * time.Hour
)

// SendWhenFreeDeps holds dependencies for the channel_send_when_free tool.
type SendWhenFreeDeps struct {
	// Links returns the current outbound link map. Same as SendDeps.Links.
	Links func() map[string][]channel.Link

	// Output receives cross-channel messages for immediate delivery.
	Output chan<- channel.TaggedMessage

	// Channels resolves the current set of live channels.
	Channels func() map[channel.ChannelID]channel.Channel

	// ActiveChannel returns the name of the channel currently being processed.
	ActiveChannel func() string

	// ActivityTracker checks whether target channels are busy.
	ActivityTracker *channel.ActivityTracker

	// PendingStore persists queued messages for deferred delivery.
	PendingStore *channel.PendingStore
}

// RegisterSendWhenFreeTool adds the channel_send_when_free tool to the MCP handler.
func RegisterSendWhenFreeTool(handler *mcp.Handler, deps SendWhenFreeDeps) {
	handler.Register(channelSendWhenFreeDef(), channelSendWhenFreeHandler(deps))
}

const ToolChannelSendWhenFree = "channel_send_when_free"

func channelSendWhenFreeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelSendWhenFree,
		Description: "Send a message to another channel, waiting until it's free if currently busy. " +
			"If the target channel is free, delivers immediately. If busy, the message is queued " +
			"durably (survives restarts) and delivered automatically when the target becomes free. " +
			"If the timeout expires before the target is free, the message is delivered anyway with " +
			"a [delayed] prefix. Use this instead of channel_send when you don't want to interrupt " +
			"an ongoing conversation.",
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
				},
				"timeout_minutes": {
					"type": "integer",
					"description": "How many minutes to wait before delivering anyway. Defaults to 30, max 120."
				}
			},
			"required": ["from_channel", "to_channel", "message"]
		}`),
	}
}

type channelSendWhenFreeParams struct {
	FromChannel    string `json:"from_channel"`
	ToChannel      string `json:"to_channel"`
	Message        string `json:"message"`
	TimeoutMinutes int    `json:"timeout_minutes"`
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

		// Validate from_channel matches the active channel.
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

		// If the target is free, deliver immediately.
		if deps.ActivityTracker != nil && !deps.ActivityTracker.IsBusy(p.ToChannel) {
			if err := deliverMessage(deps, p.FromChannel, p.ToChannel, p.Message); err != nil {
				return nil, fmt.Errorf("deliver message: %w", err)
			}
			return json.Marshal(map[string]string{
				"status":  "sent",
				"from":    p.FromChannel,
				"to":      p.ToChannel,
				"message": p.Message,
			})
		}

		// Target is busy — queue for deferred delivery.
		timeout := defaultSendTimeout
		if p.TimeoutMinutes > 0 {
			timeout = time.Duration(p.TimeoutMinutes) * time.Minute
			if timeout > maxSendTimeout {
				timeout = maxSendTimeout
			}
		}

		pending := channel.PendingMessage{
			ID:          fmt.Sprintf("pending_%d", time.Now().UnixNano()),
			FromChannel: p.FromChannel,
			ToChannel:   p.ToChannel,
			Message:     p.Message,
			QueuedAt:    time.Now(),
			ExpiresAt:   time.Now().Add(timeout),
		}

		if err := deps.PendingStore.Add(ctx, pending); err != nil {
			return nil, fmt.Errorf("queue message for deferred delivery: %w", err)
		}

		return json.Marshal(map[string]string{
			"status":     "queued",
			"pending_id": pending.ID,
			"from":       p.FromChannel,
			"to":         p.ToChannel,
			"expires_at": pending.ExpiresAt.Format(time.RFC3339),
			"message":    fmt.Sprintf("Target channel %q is busy. Message queued and will be delivered when free (or after %d minutes).", p.ToChannel, int(timeout.Minutes())),
		})
	}
}

// deliverMessage injects a cross-channel message into the output channel.
func deliverMessage(deps SendWhenFreeDeps, from, to, message string) error {
	channels := deps.Channels()
	if channels == nil {
		return fmt.Errorf("no channels available")
	}

	var targetID channel.ChannelID
	found := false
	for _, ch := range channels {
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
	case deps.Output <- msg:
		return nil
	default:
		return fmt.Errorf("message buffer full for channel %q", to)
	}
}
