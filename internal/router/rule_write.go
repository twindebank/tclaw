package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tclaw/internal/channel"
	"tclaw/internal/memorylayout"
	"tclaw/internal/tool/ruletools"
)

// maxRulePromptChars caps how much of a proposed rulebook goes into the chat.
// The user has to be able to read what they are approving on a phone; past that
// the prompt names the file and says how long it is instead.
const maxRulePromptChars = 1200

// RuleWritePayload is the proposed rulebook, held until the user answers.
type RuleWritePayload struct {
	File    string `json:"file"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

// confirmRuleWrite writes the approved rulebook. It runs outside the sandbox, so
// this is the only path by which a rulebook changes — the agent's own writes are
// refused by the rules-gate hook.
func confirmRuleWrite(ctx context.Context, chID channel.ChannelID, chName string, pending channel.PendingAction, params confirmParams) bool {
	notify := func(text string) {
		if params.Notify != nil {
			params.Notify(ctx, chID, text)
		}
	}

	var payload RuleWritePayload
	if err := json.Unmarshal(pending.Payload, &payload); err != nil {
		slog.Error("rule write: failed to decode approved payload", "channel", chName, "err", err)
		notify("❌ Something went wrong reading that rule change, so nothing was written. Ask me to propose it again.")
		return true
	}
	if params.MemoryDir == "" {
		slog.Error("rule write: no memory dir configured", "channel", chName, "file", payload.File)
		notify("❌ There is nowhere to write rulebooks on this instance, so nothing was saved.")
		return true
	}

	rulesDir := memorylayout.RulesDir(params.MemoryDir)
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		slog.Error("rule write: failed to create rules dir", "dir", rulesDir, "err", err)
		notify("❌ Could not create the rules directory, so nothing was written.")
		return true
	}

	// The tool validated the name, but this runs on a payload that has sat in
	// storage since, and it writes outside the sandbox.
	if payload.File != filepath.Base(payload.File) || filepath.Ext(payload.File) != ".md" {
		slog.Error("rule write: refusing an unsafe file name", "channel", chName, "file", payload.File)
		notify("❌ That rulebook name isn't valid, so nothing was written.")
		return true
	}

	path := filepath.Join(rulesDir, payload.File)
	existed := false
	if _, err := os.Stat(path); err == nil {
		existed = true
	}
	if err := os.WriteFile(path, []byte(payload.Content), 0o600); err != nil {
		slog.Error("rule write: failed to write rulebook", "path", path, "err", err)
		notify("❌ Could not write " + payload.File + ", so nothing changed.")
		return true
	}

	verb := "Created"
	if existed {
		verb = "Updated"
	}
	slog.Info("rule write confirmed", "channel", chName, "file", payload.File, "created", !existed)
	notify(fmt.Sprintf("📓 %s %s. It applies from the next turn — add it to a channel's CLAUDE.md to have it load automatically there.", verb, payload.File))
	return true
}

// armRuleWriteParams holds what arming a rulebook confirmation needs.
type armRuleWriteParams struct {
	RuntimeState  *channel.RuntimeStateStore
	ActiveChannel func() string
	Channels      func() map[channel.ChannelID]channel.Channel
	Send          func(context.Context, channel.ChannelID, string, channel.SendOpts) (channel.MessageID, error)
}

// newRuleWriteArmer returns the hook rule_propose calls to ask the user to
// approve a rulebook.
//
// The prompt goes to the chat directly rather than through the agent, and the
// pending action is written before it is sent: if the prompt went first, a fast
// "yes" could arrive while nothing was armed, slip past the intercept and reach
// the agent — which would then answer its own prompt.
func newRuleWriteArmer(params armRuleWriteParams) func(context.Context, ruletools.RuleWriteRequest) error {
	return func(ctx context.Context, request ruletools.RuleWriteRequest) error {
		chName := ""
		if params.ActiveChannel != nil {
			chName = params.ActiveChannel()
		}
		if chName == "" {
			return fmt.Errorf("no active channel to ask for confirmation on")
		}

		var chID channel.ChannelID
		for id, ch := range params.Channels() {
			if ch.Info().Name == chName {
				chID = id
				break
			}
		}
		if chID == "" {
			return fmt.Errorf("channel %q is not live, so nobody can be asked", chName)
		}

		payload, err := json.Marshal(RuleWritePayload{
			File:    request.File,
			Content: request.Content,
			Reason:  request.Reason,
		})
		if err != nil {
			return fmt.Errorf("encode rule change: %w", err)
		}

		if err := params.RuntimeState.Update(ctx, chName, func(rs *channel.RuntimeState) {
			rs.PendingAction = channel.NewPendingAction(channel.PendingRuleWrite, payload)
		}); err != nil {
			return fmt.Errorf("arm rule confirmation: %w", err)
		}

		if _, err := params.Send(ctx, chID, ruleWritePrompt(request), channel.SendOpts{}); err != nil {
			// Roll back so the channel isn't left armed for a change the user
			// was never actually asked about.
			if clearErr := params.RuntimeState.Update(ctx, chName, func(rs *channel.RuntimeState) {
				rs.PendingAction = nil
			}); clearErr != nil {
				slog.Error("failed to disarm rule confirmation after prompt send failure",
					"channel", chName, "file", request.File, "err", clearErr)
			}
			return fmt.Errorf("send confirmation prompt: %w", err)
		}

		slog.Info("rule change confirmation sent", "file", request.File, "channel", chName)
		return nil
	}
}

// ruleWritePrompt shows the user what they are approving. The text itself is the
// decision, so it is quoted rather than described — a summary would mean
// approving one thing and getting another.
func ruleWritePrompt(request ruletools.RuleWriteRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📓 Save this to %s?\n\n", request.File)
	fmt.Fprintf(&b, "Why: %s\n\n", request.Reason)

	content := request.Content
	if len(content) > maxRulePromptChars {
		fmt.Fprintf(&b, "The full text is %d characters, too long to show here. It starts:\n\n", len(content))
		content = content[:maxRulePromptChars] + "\n…"
	}
	b.WriteString(content)
	b.WriteString("\n\nReply \"yes\" to save it. Anything else declines.")
	return b.String()
}
