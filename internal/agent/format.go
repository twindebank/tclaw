package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// formatToolUse renders a tool invocation with its arguments.
// Prefixed with a newline so it doesn't run into preceding text.
func formatToolUse(block claudecli.ContentBlock) string {
	if len(block.Input) == 0 || string(block.Input) == "{}" {
		return fmt.Sprintf("\n🔧 %s\n", block.Name)
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

	return fmt.Sprintf("\n🔧 %s(%s)\n", block.Name, strings.Join(parts, ", "))
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
