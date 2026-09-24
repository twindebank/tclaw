package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/mcp"
)

// ToolPermissionPrompt is the MCP tool the CLI calls, through --permission-prompt-tool, when a
// tool call needs the user's approval. The model is kept from calling it with --disallowedTools.
const ToolPermissionPrompt = "permission_prompt"

// permissionPromptTimeout is how long a turn waits for an answer before the call is refused.
// It stays under the CLI's five-minute limit on a silent MCP call, and the user's other
// channels wait behind the turn for as long as it runs.
const permissionPromptTimeout = 4 * time.Minute

// permissionPrompts holds the approvals the CLI is waiting on, by prompt id. A button press
// reaches it from the router's message bridge, which reads while a turn runs; the agent's
// own queue only reads between turns.
type permissionPrompts struct {
	mu      sync.Mutex
	waiting map[string]chan channel.PromptReply
}

func newPermissionPrompts() *permissionPrompts {
	return &permissionPrompts{waiting: make(map[string]chan channel.PromptReply)}
}

// resolve answers a waiting approval with the user's button press. It reports whether msg
// was such a press, which is then consumed rather than passed on.
func (p *permissionPrompts) resolve(msg channel.TaggedMessage) bool {
	if msg.SourceInfo != nil && msg.SourceInfo.Source != channel.SourceUser {
		return false
	}
	press := channel.ParseButtonPress(msg.Text)
	if press == nil {
		return false
	}
	p.mu.Lock()
	reply, ok := p.waiting[press.PromptID]
	delete(p.waiting, press.PromptID)
	p.mu.Unlock()
	if !ok {
		return false
	}
	// Buffered, so a press after the wait gave up is dropped rather than blocking the bridge.
	reply <- press.Reply
	return true
}

func (p *permissionPrompts) await(promptID string) chan channel.PromptReply {
	reply := make(chan channel.PromptReply, 1)
	p.mu.Lock()
	p.waiting[promptID] = reply
	p.mu.Unlock()
	return reply
}

func (p *permissionPrompts) forget(promptID string) {
	p.mu.Lock()
	delete(p.waiting, promptID)
	p.mu.Unlock()
}

// permissionPromptParams is what the tool needs to find the channel to ask on.
type permissionPromptParams struct {
	Prompts       *permissionPrompts
	ActiveChannel func() string
	Channels      func() map[channel.ChannelID]channel.Channel
}

// permissionRequest is what the CLI sends the prompt tool.
type permissionRequest struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// permissionDecision is what the prompt tool answers. Allow passes the input back unchanged.
type permissionDecision struct {
	Behavior     permissionBehavior `json:"behavior"`
	UpdatedInput json.RawMessage    `json:"updatedInput,omitempty"`
	Message      string             `json:"message,omitempty"`
}

type permissionBehavior string

const (
	permissionAllow permissionBehavior = "allow"
	permissionDeny  permissionBehavior = "deny"
)

func registerPermissionPrompt(handler *mcp.Handler, p permissionPromptParams) {
	handler.Register(mcp.ToolDef{
		Name:        ToolPermissionPrompt,
		Description: "Asks the user to approve a tool call. Called by the CLI, not by you.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"tool_name":{"type":"string"},"input":{"type":"object"},"tool_use_id":{"type":"string"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var request permissionRequest
		if err := json.Unmarshal(args, &request); err != nil {
			return nil, fmt.Errorf("invalid permission request: %w", err)
		}
		return json.Marshal(askPermission(ctx, p, request))
	})
}

// askPermission puts the tool call to the user and waits for their press. Anything but an
// approval, including no answer in time, refuses the call.
func askPermission(ctx context.Context, p permissionPromptParams, request permissionRequest) permissionDecision {
	channels := p.Channels()
	chID := channelIDByName(channels, p.ActiveChannel())
	ch := channels[chID]
	if ch == nil {
		slog.Error("permission prompt with no live channel to ask on", "tool", request.ToolName)
		return permissionDecision{Behavior: permissionDeny, Message: "There was no channel to ask the user on, so the call was refused."}
	}
	if _, ok := ch.(channel.Prompter); !ok {
		// A typed reply would wait in the queue until this turn ends, so without
		// buttons there is no way to answer in time.
		return permissionDecision{Behavior: permissionDeny, Message: "This channel cannot ask for approval during a turn, so the call was refused."}
	}

	promptID := channel.NewPromptID()
	reply := p.Prompts.await(promptID)
	defer p.Prompts.forget(promptID)
	if err := channel.Ask(ctx, channel.AskParams{
		Channel:  ch,
		Text:     permissionPromptText(request),
		PromptID: promptID,
		SendText: func(context.Context, string) error { return fmt.Errorf("channel %s has no buttons", chID) },
	}); err != nil {
		slog.Error("failed to ask for tool approval", "channel", chID, "tool", request.ToolName, "err", err)
		return permissionDecision{Behavior: permissionDeny, Message: "The approval prompt could not be sent, so the call was refused."}
	}

	select {
	case answer := <-reply:
		if answer != channel.ReplyYes {
			return permissionDecision{Behavior: permissionDeny, Message: "The user declined this tool call."}
		}
		return permissionDecision{Behavior: permissionAllow, UpdatedInput: request.Input}
	case <-ctx.Done():
		return permissionDecision{Behavior: permissionDeny, Message: "The turn ended before the user answered."}
	case <-time.After(permissionPromptTimeout):
		return permissionDecision{Behavior: permissionDeny, Message: "The user did not answer within 4 minutes, so the call was refused."}
	}
}

// channelIDByName returns the id of the live channel with the given name, or "" when there is none.
func channelIDByName(channels map[channel.ChannelID]channel.Channel, name string) channel.ChannelID {
	for id, ch := range channels {
		if ch.Info().Name == name {
			return id
		}
	}
	return ""
}

// permissionPromptInputMax caps how much of a tool's input the prompt shows.
const permissionPromptInputMax = 600

// permissionPromptText names the tool and shows what it would be called with, so the user is
// approving a specific call rather than a tool in general.
func permissionPromptText(request permissionRequest) string {
	// Shown as inline code, which a backtick in the input would end early.
	input := strings.ReplaceAll(strings.TrimSpace(string(request.Input)), "`", "'")
	if runes := []rune(input); len(runes) > permissionPromptInputMax {
		input = string(runes[:permissionPromptInputMax]) + "…"
	}
	return fmt.Sprintf("🔐 Allow **%s**?\n\n`%s`", request.ToolName, input)
}
