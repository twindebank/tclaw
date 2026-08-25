package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/internal/claudecli"
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

// Icons that head a status line, so a skill invocation is distinguishable from an
// ordinary tool call at a glance.
const (
	iconTool  = "🔧"
	iconSkill = "🎓"
)

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

// truncateValue keeps an argument value within 60 bytes for a one-line status
// message, cutting on a rune boundary so a multi-byte character can't be split.
func truncateValue(s string) string {
	const maxLen = 60
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
