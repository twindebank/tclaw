package telegram_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/telegram"
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
		{"underscore tag stripped", "<quoted_message>some text</quoted_message>", "some text"},
		{"underscore tag with attributes stripped", `<quoted_message attr="val">content</quoted_message>`, "content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := telegram.SanitizeHTML(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestStripAllTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain text unchanged", "hello world", "hello world"},
		{"strips bold", "<b>bold</b> text", "bold text"},
		{"strips nested tags", "<blockquote expandable><b>text</b></blockquote>", "text"},
		{"strips link", `<a href="https://example.com">link</a>`, "link"},
		{"preserves bare angle brackets", "if x < 5", "if x < 5"},
		{"strips underscore tags", "<quoted_message>quoted</quoted_message>", "quoted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, telegram.StripAllTags(tt.input))
		})
	}
}

func TestMarkdownToHTML(t *testing.T) {
	t.Run("converts markdown link", func(t *testing.T) {
		require.Equal(t, `<a href="https://example.com">Example</a>`, telegram.MarkdownToHTML(`[Example](https://example.com)`))
	})

	t.Run("converts link even when HTML bold is present", func(t *testing.T) {
		// Links must be converted unconditionally — the presence of <b> should not block them.
		require.Equal(t, `<b>Title</b> <a href="https://example.com">link</a>`, telegram.MarkdownToHTML(`<b>Title</b> [link](https://example.com)`))
	})

	t.Run("converts bold when no HTML present", func(t *testing.T) {
		require.Equal(t, "<b>bold</b>", telegram.MarkdownToHTML("**bold**"))
	})

	t.Run("skips bold conversion when HTML already present", func(t *testing.T) {
		// Model used <b> correctly — don't double-convert.
		require.Equal(t, "<b>title</b> **not converted**", telegram.MarkdownToHTML("<b>title</b> **not converted**"))
	})

	t.Run("converts inline code when no HTML present", func(t *testing.T) {
		require.Equal(t, "<code>fmt.Println</code>", telegram.MarkdownToHTML("`fmt.Println`"))
	})

	t.Run("converts dash bullet points", func(t *testing.T) {
		require.Equal(t, "• item one\n• item two", telegram.MarkdownToHTML("- item one\n- item two"))
	})

	t.Run("converts asterisk bullet points", func(t *testing.T) {
		require.Equal(t, "• item", telegram.MarkdownToHTML("* item"))
	})

	t.Run("converts ATX heading to bold", func(t *testing.T) {
		require.Equal(t, "<b>Restaurant Name</b>", telegram.MarkdownToHTML("## Restaurant Name"))
	})

	t.Run("converts link with balanced parentheses in URL", func(t *testing.T) {
		// Wikipedia-style URLs contain parentheses.
		input := `[Shinjuku](https://en.wikipedia.org/wiki/Shinjuku_(ward))`
		require.Equal(t, `<a href="https://en.wikipedia.org/wiki/Shinjuku_(ward)">Shinjuku</a>`, telegram.MarkdownToHTML(input))
	})

	t.Run("escapes ampersand in link URL", func(t *testing.T) {
		// Bare & in href attributes breaks Telegram's HTML parser.
		input := `[search](https://example.com/search?q=foo&page=2)`
		require.Equal(t, `<a href="https://example.com/search?q=foo&amp;page=2">search</a>`, telegram.MarkdownToHTML(input))
	})

	t.Run("real-world web search result with mixed markdown", func(t *testing.T) {
		input := "## Soft Launch\n\n- Great food\n- [Book here](https://restaurant.com)\n\n**Highly recommended**"
		got := telegram.MarkdownToHTML(input)
		require.Contains(t, got, "<b>Soft Launch</b>")
		require.Contains(t, got, "• Great food")
		require.Contains(t, got, `<a href="https://restaurant.com">Book here</a>`)
		require.Contains(t, got, "<b>Highly recommended</b>")
	})
}

func TestSanitizeUTF8(t *testing.T) {
	t.Run("valid UTF-8 unchanged", func(t *testing.T) {
		require.Equal(t, "hello world", telegram.SanitizeUTF8("hello world"))
	})

	t.Run("empty string unchanged", func(t *testing.T) {
		require.Equal(t, "", telegram.SanitizeUTF8(""))
	})

	t.Run("invalid bytes stripped", func(t *testing.T) {
		// Embed a raw invalid UTF-8 byte sequence in an otherwise valid string.
		input := "hello\xff\xfeworld"
		got := telegram.SanitizeUTF8(input)
		require.Equal(t, "helloworld", got)
	})

	t.Run("valid unicode emoji unchanged", func(t *testing.T) {
		require.Equal(t, "hello 🌍", telegram.SanitizeUTF8("hello 🌍"))
	})

	t.Run("mixed valid and invalid bytes", func(t *testing.T) {
		// Only the invalid bytes are dropped; valid UTF-8 sequences are preserved.
		input := "valid\x80text\xc0more"
		got := telegram.SanitizeUTF8(input)
		require.Equal(t, "validtextmore", got)
	})
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
