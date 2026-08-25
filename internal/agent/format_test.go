package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"tclaw/internal/claudecli"
)

func TestFormatToolUse(t *testing.T) {
	t.Run("skill use is led by the skill name under its own icon", func(t *testing.T) {
		got := formatToolUse(claudecli.ContentBlock{
			Type:  claudecli.ContentToolUse,
			Name:  string(claudecli.ToolSkill),
			Input: json.RawMessage(`{"skill":"channel-rules"}`),
		})

		require.Equal(t, "\n🎓 channel-rules\n", got, "skill name should head the line")
	})

	t.Run("skill args are shown in brackets", func(t *testing.T) {
		got := formatToolUse(claudecli.ContentBlock{
			Type:  claudecli.ContentToolUse,
			Name:  string(claudecli.ToolSkill),
			Input: json.RawMessage(`{"skill":"channel-rules","args":"list"}`),
		})

		require.Equal(t, "\n🎓 channel-rules (list)\n", got, "args should follow the skill name")
	})

	t.Run("skill use with no name falls back to the tool name", func(t *testing.T) {
		got := formatToolUse(claudecli.ContentBlock{
			Type:  claudecli.ContentToolUse,
			Name:  string(claudecli.ToolSkill),
			Input: json.RawMessage(`{"args":"list"}`),
		})

		require.Equal(t, "\n🎓 Skill\n", got, "the skill icon should survive an unreadable name")
	})

	t.Run("skill use with unparseable input falls back to the tool name", func(t *testing.T) {
		got := formatToolUse(claudecli.ContentBlock{
			Type:  claudecli.ContentToolUse,
			Name:  string(claudecli.ToolSkill),
			Input: json.RawMessage(`not json`),
		})

		require.Equal(t, "\n🎓 Skill\n", got, "bad input should not lose the skill icon")
	})

	t.Run("an ordinary tool keeps the tool icon and its arguments", func(t *testing.T) {
		got := formatToolUse(claudecli.ContentBlock{
			Type:  claudecli.ContentToolUse,
			Name:  string(claudecli.ToolRead),
			Input: json.RawMessage(`{"file_path":"/tmp/x.go"}`),
		})

		require.Equal(t, "\n🔧 Read(file_path=/tmp/x.go)\n", got, "non-skill tools are unchanged")
	})

	t.Run("an ordinary tool with no arguments shows just its name", func(t *testing.T) {
		got := formatToolUse(claudecli.ContentBlock{
			Type:  claudecli.ContentToolUse,
			Name:  string(claudecli.ToolBash),
			Input: json.RawMessage(`{}`),
		})

		require.Equal(t, "\n🔧 Bash\n", got, "an empty input object should not render brackets")
	})
}

func TestTruncateValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short value is untouched",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "value at the limit is untouched",
			input:    strings.Repeat("0123456789", 6),
			expected: strings.Repeat("0123456789", 6),
		},
		{
			name:     "long value is cut and marked",
			input:    strings.Repeat("0123456789", 8),
			expected: "012345678901234567890123456789012345678901234567890123456...",
		},
		{
			name: "multi-byte characters are not split",
			// A pound sign is two bytes, so 28 of them plus the three dots is
			// 59 bytes; a 29th would take the line to 61 and overrun.
			input:    strings.Repeat("£", 65),
			expected: strings.Repeat("£", 28) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateValue(tt.input)
			require.Equal(t, tt.expected, got, "truncation of %q", tt.input)
			require.True(t, utf8.ValidString(got), "result must stay valid UTF-8")
			require.LessOrEqual(t, len(got), 60, "result must fit the 60-byte budget")
		})
	}
}
