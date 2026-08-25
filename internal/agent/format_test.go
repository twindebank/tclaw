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

func TestFormatToolResult(t *testing.T) {
	t.Run("a refusal names tclaw's own hook and the tool it stopped", func(t *testing.T) {
		// The text is a real refusal captured from the CLI, path and all.
		got := formatToolResult(toolResult(t,
			`Error: PreToolUse:Write hook error: ["/usr/local/bin/tclaw-hooks" rules-gate]: `+
				"Refused: git.md is a rulebook, and rulebooks are the user's standing decisions."+
				"\n\nUse `rule_propose` with the full text you want the file to have.\n"))

		require.Equal(t,
			"  ↳ 🪝 rules-gate blocked Write: Refused: git.md is a rulebook, and rulebooks are the user's standing decisions.\n",
			got)
	})

	t.Run("a hook that isn't tclaw's is reported without a name", func(t *testing.T) {
		got := formatToolResult(toolResult(t,
			"Error: PreToolUse:Bash hook error: [echo nope >&2; exit 2]: nope\n"))

		require.Equal(t, "  ↳ 🪝 a PreToolUse hook blocked Bash: nope\n", got)
	})

	t.Run("a long reason is cut to fit one status line", func(t *testing.T) {
		got := formatToolResult(toolResult(t,
			"Error: PreToolUse:Edit hook error: [hook]: "+strings.Repeat("0123456789", 20)))

		want := "  ↳ 🪝 a PreToolUse hook blocked Edit: " + strings.Repeat("0123456789", 11) + "0123456...\n"
		require.Equal(t, want, got, "an over-long reason should be cut and marked")
	})

	t.Run("a refusal with no reason still says what was blocked", func(t *testing.T) {
		got := formatToolResult(toolResult(t, "Error: PreToolUse:Write hook error: [hook]: \n"))

		require.Equal(t, "  ↳ 🪝 a PreToolUse hook blocked Write\n", got)
	})

	t.Run("an ordinary string result is unchanged", func(t *testing.T) {
		got := formatToolResult(toolResult(t, "hello"))

		require.Equal(t, "  ↳ Done (7 B)\n", got, "a plain result should still report its size")
	})

	t.Run("an object result still reports its stats", func(t *testing.T) {
		got := formatToolResult(json.RawMessage(`{"durationSeconds":1.5}`))

		require.Contains(t, got, "1.5s", "duration should survive")
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

// --- helpers ---

// toolResult encodes text the way the CLI carries a string tool result.
func toolResult(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(text)
	require.NoError(t, err)
	return raw
}
