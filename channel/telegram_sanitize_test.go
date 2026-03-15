package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeTelegramHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "plain text",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "allowed bold tag",
			input:    "<b>bold</b>",
			expected: "<b>bold</b>",
		},
		{
			name:     "allowed italic tags",
			input:    "<i>italic</i> <em>emphasis</em>",
			expected: "<i>italic</i> <em>emphasis</em>",
		},
		{
			name:     "allowed underline tags",
			input:    "<u>underline</u> <ins>inserted</ins>",
			expected: "<u>underline</u> <ins>inserted</ins>",
		},
		{
			name:     "allowed strikethrough tags",
			input:    "<s>strike</s> <strike>strike</strike> <del>deleted</del>",
			expected: "<s>strike</s> <strike>strike</strike> <del>deleted</del>",
		},
		{
			name:     "allowed code and pre",
			input:    "<code>inline</code> <pre>block</pre>",
			expected: "<code>inline</code> <pre>block</pre>",
		},
		{
			name:     "allowed link",
			input:    `<a href="https://example.com">link</a>`,
			expected: `<a href="https://example.com">link</a>`,
		},
		{
			name:     "allowed blockquote",
			input:    "<blockquote>quoted</blockquote>",
			expected: "<blockquote>quoted</blockquote>",
		},
		{
			name:     "blockquote expandable preserved",
			input:    "<blockquote expandable>collapsed</blockquote>",
			expected: "<blockquote expandable>collapsed</blockquote>",
		},
		{
			name:     "pre with language attribute",
			input:    `<pre language="python">def foo(): pass</pre>`,
			expected: `<pre language="python">def foo(): pass</pre>`,
		},
		{
			name:     "tg-spoiler preserved",
			input:    "<tg-spoiler>hidden</tg-spoiler>",
			expected: "<tg-spoiler>hidden</tg-spoiler>",
		},
		{
			name:     "tg-emoji preserved",
			input:    `<tg-emoji emoji-id="12345">🎉</tg-emoji>`,
			expected: `<tg-emoji emoji-id="12345">🎉</tg-emoji>`,
		},
		{
			name:     "unsupported tag stripped, text preserved",
			input:    "<userdir>/home/user</userdir>",
			expected: "/home/user",
		},
		{
			name:     "unsupported br stripped",
			input:    "line one<br>line two",
			expected: "line oneline two",
		},
		{
			name:     "unsupported self-closing br",
			input:    "line one<br/>line two",
			expected: "line one&lt;br/>line two",
		},
		{
			name:     "unsupported div stripped",
			input:    "<div>content</div>",
			expected: "content",
		},
		{
			name:     "unsupported span stripped",
			input:    "hello <span class=\"red\">world</span>",
			expected: "hello world",
		},
		{
			name:     "unsupported p and h1",
			input:    "<p>paragraph</p><h1>heading</h1>",
			expected: "paragraphheading",
		},
		{
			name:     "bare angle bracket escaped",
			input:    "if x < 5 then",
			expected: "if x &lt; 5 then",
		},
		{
			name:     "bare angle bracket not followed by alpha",
			input:    "a < 3 > b",
			expected: "a &lt; 3 > b",
		},
		{
			name:     "code block passthrough preserves inner HTML",
			input:    "<code><div>test</div></code>",
			expected: "<code><div>test</div></code>",
		},
		{
			name:     "pre block passthrough preserves inner HTML",
			input:    "<pre><div>test</div></pre>",
			expected: "<pre><div>test</div></pre>",
		},
		{
			name:     "pre with nested code passthrough",
			input:    `<pre><code class="language-go">if x < 5 { fmt.Println("hi") }</code></pre>`,
			expected: `<pre><code class="language-go">if x < 5 { fmt.Println("hi") }</code></pre>`,
		},
		{
			name:     "mixed valid and invalid tags",
			input:    "<b>bold</b> <unsupported>text</unsupported> <i>italic</i>",
			expected: "<b>bold</b> text <i>italic</i>",
		},
		{
			name:     "status wrap with invalid content inside",
			input:    "<blockquote expandable>🤔 Thinking...\n<userdir>/home</userdir>\n</blockquote>",
			expected: "<blockquote expandable>🤔 Thinking...\n/home\n</blockquote>",
		},
		{
			name:     "multiple bare angle brackets",
			input:    "a < b and c < d",
			expected: "a &lt; b and c &lt; d",
		},
		{
			name:     "nested allowed tags preserved",
			input:    "<b><i>bold italic</i></b>",
			expected: "<b><i>bold italic</i></b>",
		},
		{
			name:     "unclosed code tag passthrough to end",
			input:    "<code>some code without close",
			expected: "<code>some code without close",
		},
		{
			name:     "real world: tool result with path",
			input:    "<blockquote expandable>🔧 Reading /data/tclaw/theo/home/.claude/settings.json\n✅ Tool result (143 chars)\n</blockquote>",
			expected: "<blockquote expandable>🔧 Reading /data/tclaw/theo/home/.claude/settings.json\n✅ Tool result (143 chars)\n</blockquote>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeTelegramHTML(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
