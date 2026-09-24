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

// permissionPromptTimeout is how long a call waits for an answer, inside the CLI's five-minute
// limit on a silent MCP call. The user's other channels wait behind the turn meanwhile.
const permissionPromptTimeout = 4 * time.Minute

// permissionPrompts holds the approvals the CLI is waiting on, by prompt id, for the router's
// message bridge to answer: it reads while a turn runs, and the agent's queue does not.
type permissionPrompts struct {
	mu      sync.Mutex
	waiting map[string]chan channel.PromptReply

	// unanswered is set once a prompt times out, and refuses the rest of that turn's prompts
	// at once, so a turn cannot hold the user's other channels for four minutes a call.
	unanswered bool
}

// newTurn clears what the last turn left, at the start of each one.
func (p *permissionPrompts) newTurn() {
	p.mu.Lock()
	p.unanswered = false
	p.mu.Unlock()
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

func (p *permissionPrompts) markUnanswered() {
	p.mu.Lock()
	p.unanswered = true
	p.mu.Unlock()
}

func (p *permissionPrompts) alreadyUnanswered() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unanswered
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
	prompter, ok := ch.(channel.Prompter)
	if !ok {
		// A typed reply would wait in the queue until this turn ends, so without
		// buttons there is no way to answer in time.
		return permissionDecision{Behavior: permissionDeny, Message: "This channel cannot ask for approval during a turn, so the call was refused."}
	}
	if p.Prompts.alreadyUnanswered() {
		return permissionDecision{Behavior: permissionDeny, Message: "An earlier approval this turn went unanswered, so the user is away; the call was refused."}
	}
	input := strings.TrimSpace(string(request.Input))
	if n := len([]rune(input)); n > permissionPromptInputMax {
		// The user approves what they are shown. Showing only the start would let
		// the rest of the call through unseen.
		return permissionDecision{Behavior: permissionDeny, Message: fmt.Sprintf(
			"The call's input is %d characters, too long to show the user in full for approval (the limit is %d). Split it into smaller calls.",
			n, permissionPromptInputMax)}
	}

	promptID := channel.NewPromptID()
	reply := p.Prompts.await(promptID)
	defer p.Prompts.forget(promptID)
	if _, err := prompter.SendPrompt(ctx, channel.SendPromptParams{
		Text:     permissionPromptText(request.ToolName, input),
		PromptID: promptID,
		Replies:  []channel.PromptReply{channel.ReplyYes, channel.ReplyNo},
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
		p.Prompts.markUnanswered()
		if _, err := ch.Send(ctx, fmt.Sprintf("⌛ No answer in %s, so the %s call was refused.", permissionPromptTimeout, request.ToolName), channel.SendOpts{}); err != nil {
			slog.Warn("failed to say an approval timed out", "channel", chID, "err", err)
		}
		return permissionDecision{Behavior: permissionDeny, Message: fmt.Sprintf(
			"The user did not answer within %s, so the call was refused.", permissionPromptTimeout)}
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

// permissionPromptInputMax is the longest input the prompt shows, in characters: comfortably
// inside a Telegram message with the rest of the prompt around it.
const permissionPromptInputMax = 3000

// permissionPromptText names the tool and shows the whole input it would be called with, so
// the user is approving a specific call rather than a tool in general.
func permissionPromptText(toolName, input string) string {
	// Shown as inline code, which a backtick in the input would end early.
	return fmt.Sprintf("🔐 Allow **%s**?\n\n`%s`", toolName, strings.ReplaceAll(input, "`", "'"))
}
