package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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

	// PromptID ties a button press to this prompt, so an old button cannot answer a newer one.
	PromptID string

	Replies []PromptReply
}

// Prompter is implemented by transports that can put answer buttons under a message. Only
// tclaw's own confirmation prompts are sent through it, never text the agent chose.
type Prompter interface {
	SendPrompt(ctx context.Context, p SendPromptParams) (MessageID, error)
}

// NewPromptID returns a random prompt identifier, short enough to fit in a button's data.
func NewPromptID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
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
