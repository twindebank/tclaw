package channel

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
)

// PromptReply is an answer a confirmation prompt offers as a button.
type PromptReply string

const (
	ReplyYes PromptReply = "yes"
	ReplyNo  PromptReply = "no"
)

// SendPromptParams is a confirmation prompt with a button per answer.
type SendPromptParams struct {
	Text string

	// Detail is shown exactly as given below Text, with no formatting applied: what the user
	// approves must be what they see.
	Detail string

	// PromptID ties a button press to this prompt, so an old button cannot answer a newer one.
	PromptID string

	Replies []PromptReply
}

// Prompter is implemented by transports that can put answer buttons under a message. Only
// tclaw's own confirmation prompts are sent through it, never text the agent chose.
type Prompter interface {
	SendPrompt(ctx context.Context, p SendPromptParams) (MessageID, error)
}

// AskParams is a yes/no question for the user.
type AskParams struct {
	Channel  Channel
	Text     string
	PromptID string

	// SendText sends the question as a plain message, on a channel without buttons.
	SendText func(ctx context.Context, text string) error
}

// Ask puts a yes/no question to the user, with buttons where the channel has them. Typing the
// answer works either way.
func Ask(ctx context.Context, p AskParams) error {
	prompter, ok := p.Channel.(Prompter)
	if !ok {
		return p.SendText(ctx, p.Text)
	}
	if _, err := prompter.SendPrompt(ctx, SendPromptParams{Text: p.Text, PromptID: p.PromptID, Replies: []PromptReply{ReplyYes, ReplyNo}}); err != nil {
		return fmt.Errorf("send prompt with buttons: %w", err)
	}
	return nil
}

// OpenPrompts is which channels have one of the agent's own prompts open, such as a tool
// approval. Safe to use from more than one goroutine.
type OpenPrompts struct {
	mu   sync.Mutex
	open map[ChannelID]bool
}

func NewOpenPrompts() *OpenPrompts {
	return &OpenPrompts{open: make(map[ChannelID]bool)}
}

// Set records whether a prompt is open on chID.
func (o *OpenPrompts) Set(chID ChannelID, open bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if open {
		o.open[chID] = true
		return
	}
	delete(o.open, chID)
}

func (o *OpenPrompts) IsOpen(chID ChannelID) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.open[chID]
}

// NewPromptID returns a random prompt identifier, short enough to fit in a button's data.
func NewPromptID() string {
	return rand.Text()
}

// ButtonPress is a user pressing one of a prompt's buttons.
type ButtonPress struct {
	PromptID string
	Reply    PromptReply
}

// buttonPressPrefix starts the message a transport delivers for a button press.
const buttonPressPrefix = "🔘 "

// ButtonPressText is the message a transport delivers when the user presses a prompt's button.
// It reads as the answer given, and names the prompt it answers.
func ButtonPressText(p ButtonPress) string {
	return fmt.Sprintf("%s%s (prompt %s)", buttonPressPrefix, p.Reply, p.PromptID)
}

// ParseButtonPress reads a message a transport delivered for a button press. Nil means the
// message is not one.
func ParseButtonPress(text string) *ButtonPress {
	rest, ok := strings.CutPrefix(text, buttonPressPrefix)
	if !ok {
		return nil
	}
	reply, id, ok := strings.Cut(rest, " (prompt ")
	if !ok || !strings.HasSuffix(id, ")") {
		return nil
	}
	return &ButtonPress{PromptID: strings.TrimSuffix(id, ")"), Reply: PromptReply(reply)}
}
