package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

func formatBlock(block ContentBlock) string {
	switch block.Type {
	case ContentText:
		return block.Text
	case ContentThinking:
		if block.Thinking == "" {
			return ""
		}
		return "💭 " + block.Thinking + "\n"
	case ContentToolUse:
		return formatToolUse(block)
	}
	return ""
}

// formatToolUse renders a tool invocation with its arguments.
func formatToolUse(block ContentBlock) string {
	if len(block.Input) == 0 || string(block.Input) == "{}" {
		return fmt.Sprintf("🔧 %s\n", block.Name)
	}

	var args map[string]json.RawMessage
	if err := json.Unmarshal(block.Input, &args); err != nil {
		return fmt.Sprintf("🔧 %s\n", block.Name)
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
		// Truncate long values.
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, s))
	}

	return fmt.Sprintf("🔧 %s(%s)\n", block.Name, strings.Join(parts, ", "))
}

// formatToolResult renders execution stats from a tool result event.
func formatToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "  ↳ Done\n"
	}

	var meta ToolResultMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		slog.Debug("could not parse tool result meta", "err", err)
		return "  ↳ Done\n"
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
