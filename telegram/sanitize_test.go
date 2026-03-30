package telegram_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/telegram"
)

func TestSanitizeHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"plain text", "hello world", "hello world"},
		{"allowed bold tag", "<b>bold</b>", "<b>bold</b>"},
		{"allowed italic tags", "<i>italic</i> <em>emphasis</em>", "<i>italic</i> <em>emphasis</em>"},
		{"allowed underline tags", "<u>underline</u> <ins>inserted</ins>", "<u>underline</u> <ins>inserted</ins>"},
		{"allowed strikethrough tags", "<s>strike</s> <strike>strike</strike> <del>deleted</del>", "<s>strike</s> <strike>strike</strike> <del>deleted</del>"},
		{"allowed code and pre", "<code>inline</code> <pre>block</pre>", "<code>inline</code> <pre>block</pre>"},
		{"allowed link", `<a href="https://example.com">link</a>`, `<a href="https://example.com">link</a>`},
		{"allowed blockquote", "<blockquote>quoted</blockquote>", "<blockquote>quoted</blockquote>"},
		{"blockquote expandable preserved", "<blockquote expandable>collapsed</blockquote>", "<blockquote expandable>collapsed</blockquote>"},
		{"pre with language attribute", `<pre language="python">def foo(): pass</pre>`, `<pre language="python">def foo(): pass</pre>`},
		{"tg-spoiler preserved", "<tg-spoiler>hidden</tg-spoiler>", "<tg-spoiler>hidden</tg-spoiler>"},
		{"tg-emoji preserved", `<tg-emoji emoji-id="12345">🎉</tg-emoji>`, `<tg-emoji emoji-id="12345">🎉</tg-emoji>`},
		{"unsupported tag stripped, text preserved", "<userdir>/home/user</userdir>", "/home/user"},
		{"unsupported br stripped", "line one<br>line two", "line oneline two"},
		{"unsupported self-closing br", "line one<br/>line two", "line one&lt;br/>line two"},
		{"unsupported div stripped", "<div>content</div>", "content"},
		{"unsupported span stripped", "hello <span class=\"red\">world</span>", "hello world"},
		{"unsupported p and h1", "<p>paragraph</p><h1>heading</h1>", "paragraphheading"},
		{"bare angle bracket escaped", "if x < 5 then", "if x &lt; 5 then"},
		{"bare angle bracket not followed by alpha", "a < 3 > b", "a &lt; 3 > b"},
		{"code block passthrough preserves inner HTML", "<code><div>test</div></code>", "<code><div>test</div></code>"},
		{"pre block passthrough preserves inner HTML", "<pre><div>test</div></pre>", "<pre><div>test</div></pre>"},
		{"pre with nested code passthrough", `<pre><code class="language-go">if x < 5 { fmt.Println("hi") }</code></pre>`, `<pre><code class="language-go">if x < 5 { fmt.Println("hi") }</code></pre>`},
		{"mixed valid and invalid tags", "<b>bold</b> <unsupported>text</unsupported> <i>italic</i>", "<b>bold</b> text <i>italic</i>"},
		{"status wrap with invalid content inside", "<blockquote expandable>🤔 Thinking...\n<userdir>/home</userdir>\n</blockquote>", "<blockquote expandable>🤔 Thinking...\n/home\n</blockquote>"},
		{"multiple bare angle brackets", "a < b and c < d", "a &lt; b and c &lt; d"},
		{"nested allowed tags preserved", "<b><i>bold italic</i></b>", "<b><i>bold italic</i></b>"},
		{"unclosed code tag passthrough to end", "<code>some code without close", "<code>some code without close"},
		{"real world: tool result with path", "<blockquote expandable>🔧 Reading /data/tclaw/theo/home/.claude/settings.json\n✅ Tool result (143 chars)\n</blockquote>", "<blockquote expandable>🔧 Reading /data/tclaw/theo/home/.claude/settings.json\n✅ Tool result (143 chars)\n</blockquote>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := telegram.SanitizeHTML(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateSnippet(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short text unchanged", "hello world", 100, "hello world"},
		{"exact length not truncated", "12345", 5, "12345"},
		{"long text truncated with ellipsis", "abcdefghijklmnopqrstuvwxyz", 10, "abcdefghij…"},
		{"newlines collapsed to spaces", "line one\nline two", 100, "line one line two"},
		{"newlines collapsed before truncation", "aaa\nbbb\nccc", 5, "aaa b…"},
		{"empty string", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, telegram.TruncateSnippet(tt.input, tt.maxLen))
		})
	}
}
