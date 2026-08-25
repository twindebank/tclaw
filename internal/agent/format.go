package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/internal/claudecli"
	"tclaw/internal/hooks"
)

func formatBlock(block claudecli.ContentBlock) string {
	switch block.Type {
	case claudecli.ContentText:
		return block.Text
	case claudecli.ContentThinking:
		if block.Thinking == "" {
			return ""
		}
		return "💭 " + block.Thinking + "\n"
	case claudecli.ContentToolUse:
		return formatToolUse(block)
	}
	return ""
}

const (
	iconTool   = "🔧"
	iconSkill  = "🎓"
	iconHook   = "🪝"
	iconNotice = "ℹ️"
)

// noticeSpeaker separates what produced a notice from the notice itself, as in
// "PostToolUse:Write says: <notice>". The CLI repeats it on every line.
const noticeSpeaker = " says: "

// formatNotice renders a notice the CLI streamed for the user, which is how a
// hook that ran without refusing anything gets to say so. Empty when the notice
// carries nothing to show.
func formatNotice(content string) string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if line = strings.TrimSpace(stripSpeaker(line)); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}

	shown := truncate(lines[0], quoteMaxLen)
	if len(lines) > 1 {
		// This channel also carries the CLI's own banners, which run to dozens of
		// lines. They are dropped rather than filling the chat, but not silently.
		shown += fmt.Sprintf(" (+%d more)", len(lines)-1)
	}
	return fmt.Sprintf("\n%s %s\n", iconNotice, shown)
}

// stripSpeaker drops the CLI's lead-in naming what produced a notice. What it
// names never contains a space, so a notice that says "says:" itself survives.
func stripSpeaker(line string) string {
	speaker, said, found := strings.Cut(line, noticeSpeaker)
	if !found || strings.ContainsAny(speaker, " \t") {
		return line
	}
	return said
}

// formatToolUse renders a tool invocation with its arguments.
// Prefixed with a newline so it doesn't run into preceding text.
func formatToolUse(block claudecli.ContentBlock) string {
	if claudecli.Tool(block.Name) == claudecli.ToolSkill {
		return formatSkillUse(block)
	}

	if len(block.Input) == 0 || string(block.Input) == "{}" {
		return fmt.Sprintf("\n%s %s\n", iconTool, block.Name)
	}

	var args map[string]json.RawMessage
	if err := json.Unmarshal(block.Input, &args); err != nil {
		slog.Warn("failed to parse tool input", "tool", block.Name, "err", err)
		return fmt.Sprintf("\n%s %s\n", iconTool, block.Name)
	}

	var parts []string
	for k, v := range args {
		s := strings.TrimSpace(string(v))
		// Unquote simple strings for readability.
		if len(s) >= 2 && s[0] == '"' {
			var unquoted string
			if err := json.Unmarshal(v, &unquoted); err == nil {
				s = unquoted
			}
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, truncateValue(s)))
	}

	return fmt.Sprintf("\n%s %s(%s)\n", iconTool, block.Name, strings.Join(parts, ", "))
}

// formatSkillUse renders a skill invocation led by the skill's own name.
// "Skill(skill=x)" buries the only part of the line worth reading.
func formatSkillUse(block claudecli.ContentBlock) string {
	// Falls back to the tool name when the skill's own name can't be read.
	unnamed := fmt.Sprintf("\n%s %s\n", iconSkill, block.Name)

	var skill struct {
		Skill string `json:"skill"`
		Args  string `json:"args"`
	}
	if err := json.Unmarshal(block.Input, &skill); err != nil {
		slog.Warn("failed to parse skill tool input", "err", err)
		return unnamed
	}
	if skill.Skill == "" {
		slog.Warn("skill tool use carried no skill name")
		return unnamed
	}

	if skill.Args == "" {
		return fmt.Sprintf("\n%s %s\n", iconSkill, skill.Skill)
	}
	return fmt.Sprintf("\n%s %s (%s)\n", iconSkill, skill.Skill, truncateValue(skill.Args))
}

// Byte budgets for the two things a status line quotes: a tool argument, and a
// line of text from a hook or from the CLI.
const (
	argMaxLen   = 60
	quoteMaxLen = 120
)

// firstLine keeps a status line to one line of whatever it is quoting.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// truncateValue keeps an argument value within its budget for a one-line status message.
func truncateValue(s string) string {
	return truncate(s, argMaxLen)
}

// truncate cuts s to maxLen bytes, on a rune boundary so a multi-byte character
// can't be split.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxLen-3 {
			break
		}
		cut = i
	}
	return s[:cut] + "..."
}

// formatToolResult renders execution stats from a tool result event.
func formatToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "  ↳ Done\n"
	}

	// Tool results can be JSON strings, objects, or other types.
	// Only attempt to extract meta from objects.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		if refusal := parseHookRefusal(raw); refusal != nil {
			return formatHookRefusal(*refusal)
		}
		return fmt.Sprintf("  ↳ Done (%s)\n", formatBytes(len(raw)))
	}

	var meta claudecli.ToolResultMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Sprintf("  ↳ Done (%s)\n", formatBytes(len(raw)))
	}

	var parts []string
	if meta.DurationSeconds > 0 {
		parts = append(parts, fmt.Sprintf("%.1fs", meta.DurationSeconds))
	}
	// Estimate payload size from the raw JSON length.
	if size := len(raw); size > 0 {
		parts = append(parts, formatBytes(size))
	}

	if len(parts) > 0 {
		return fmt.Sprintf("  ↳ Done (%s)\n", strings.Join(parts, " · "))
	}
	return "  ↳ Done\n"
}

// hookRefusal is a tool call a PreToolUse hook refused.
type hookRefusal struct {
	// Hook is the hook's own name, empty when the command that ran doesn't name one.
	Hook string

	Tool   string
	Reason string
}

// A refused call comes back as "PreToolUse:<Tool> hook error: [<command>]: <stderr>",
// sometimes behind an "Error: " lead-in. No system event carries a refusal.
const (
	hookErrorMarker  = " hook error: "
	hookRefusalEvent = "PreToolUse:"

	// hookNoStderr is what the CLI puts in place of the reason when the hook
	// refused without writing one.
	hookNoStderr = "No stderr output"
)

// parseHookRefusal reads the CLI's report of a refused tool call out of a string
// tool result. Nil means the result is an ordinary one.
func parseHookRefusal(raw json.RawMessage) *hookRefusal {
	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}

	refused, rest, found := strings.Cut(result, hookErrorMarker)
	if !found {
		return nil
	}
	// What comes before the marker ends "PreToolUse:<Tool>", with whatever the
	// CLI prefixed ("Error: ") in front of it.
	_, tool, found := strings.Cut(refused, hookRefusalEvent)
	if !found || tool == "" {
		return nil
	}

	// The command that refused is bracketed, and the reason is its stderr.
	if !strings.HasPrefix(rest, "[") {
		return nil
	}
	command, reason, found := strings.Cut(strings.TrimPrefix(rest, "["), "]: ")
	if !found {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == hookNoStderr {
		// The hook refused and said nothing; passing that on says nothing either.
		reason = ""
	}

	return &hookRefusal{Hook: tclawHookName(command), Tool: tool, Reason: reason}
}

// tclawHookName picks tclaw's own hook name out of the command the CLI reported.
// Any other command belongs to somebody else's hook, whose text is not a name.
func tclawHookName(command string) string {
	if !strings.Contains(command, hooks.BinaryName) {
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// formatHookRefusal renders a refused tool call, so it reads as a refusal rather
// than the "Done" every other tool result gets.
func formatHookRefusal(refusal hookRefusal) string {
	who := "a PreToolUse hook"
	if refusal.Hook != "" {
		who = refusal.Hook
	}

	reason := truncate(strings.TrimSpace(firstLine(refusal.Reason)), quoteMaxLen)
	if reason == "" {
		return fmt.Sprintf("  ↳ %s %s blocked %s\n", iconHook, who, refusal.Tool)
	}
	return fmt.Sprintf("  ↳ %s %s blocked %s: %s\n", iconHook, who, refusal.Tool, reason)
}

func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
