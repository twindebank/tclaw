package telegram

import (
	"regexp"
	"strings"
)

// AllowedTags lists HTML tag names that Telegram's Bot API accepts in
// ParseMode: HTML. Everything else is stripped (text content preserved).
var AllowedTags = map[string]bool{
	"b": true, "strong": true,
	"i": true, "em": true,
	"u": true, "ins": true,
	"s": true, "strike": true, "del": true,
	"code": true, "pre": true,
	"blockquote": true,
	"a":          true,
	"tg-spoiler": true,
	"tg-emoji":   true,
}

// htmlTagPattern matches opening, closing, and self-closing HTML-like tags.
// Group 1: optional "/" for closing tags.
// Group 2: tag name.
// Group 3: rest of tag content (attributes, self-close slash, etc.).
// Includes underscore so tags like <quoted_message> are matched and stripped.
var htmlTagPattern = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9_-]*)((?:\s[^>]*)?)>`)

// markdownBold matches **text** that the model emits despite being told to use HTML.
var markdownBold = regexp.MustCompile(`\*\*(.+?)\*\*`)

// markdownInlineCode matches `text` (single backtick, not triple).
var markdownInlineCode = regexp.MustCompile("(?s)`([^`]+)`")

// markdownLink matches [text](url) — converted unconditionally since it doesn't
// overlap with HTML tags the model might already be using.
var markdownLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// markdownBullet matches a leading "- " or "* " bullet at the start of a line
// (with optional leading whitespace).
var markdownBullet = regexp.MustCompile(`(?m)^[ \t]*[-*] `)

// markdownHeader matches ATX-style Markdown headings (# through ######).
var markdownHeader = regexp.MustCompile(`(?m)^#{1,6} (.+)`)

// supportedTelegramTags is used by EscapeUnsupportedTags for quick tag filtering.
var supportedTelegramTags = map[string]bool{
	"b": true, "i": true, "u": true, "s": true,
	"code": true, "pre": true, "a": true,
	"blockquote": true, "tg-spoiler": true,
}

// htmlTagRe matches HTML-like opening and closing tags (used by EscapeUnsupportedTags).
var htmlTagRe = regexp.MustCompile(`<(/?[a-zA-Z][a-zA-Z0-9_-]*)(\s[^>]*)?>`)

// SanitizeHTML strips HTML tags not supported by Telegram's Bot API,
// preserving their text content. Supported tags pass through unchanged.
// Bare `<` characters that aren't part of recognized tags are escaped to &lt;.
// Content inside <pre> and <code> blocks is left untouched so code snippets
// containing angle brackets aren't mangled.
func SanitizeHTML(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	pos := 0
	for pos < len(s) {
		// Find the next '<' character.
		idx := strings.IndexByte(s[pos:], '<')
		if idx < 0 {
			// No more '<' — copy the rest and finish.
			out.WriteString(s[pos:])
			break
		}

		// Copy everything before the '<'.
		out.WriteString(s[pos : pos+idx])
		pos += idx

		// Try to match an HTML tag at this position.
		loc := htmlTagPattern.FindStringSubmatchIndex(s[pos:])
		if loc == nil || loc[0] != 0 {
			// Not a valid tag pattern — escape the bare '<'.
			out.WriteString("&lt;")
			pos++
			continue
		}

		fullMatch := s[pos : pos+loc[1]]
		tagName := strings.ToLower(s[pos+loc[4] : pos+loc[5]])
		isClosing := loc[3]-loc[2] > 0

		if !AllowedTags[tagName] {
			// Unsupported tag — strip it, text content flows through naturally.
			pos += loc[1]
			continue
		}

		// Allowed tag — emit it. Before emitting, check if we're entering a
		// code/pre passthrough zone where inner content should not be scanned.
		out.WriteString(fullMatch)
		pos += loc[1]

		if !isClosing && (tagName == "pre" || tagName == "code") {
			// Passthrough: copy everything verbatim until the matching close tag.
			closeTag := "</" + tagName + ">"
			endIdx := strings.Index(s[pos:], closeTag)
			if endIdx < 0 {
				// No close tag found — copy the rest verbatim.
				out.WriteString(s[pos:])
				pos = len(s)
			} else {
				out.WriteString(s[pos : pos+endIdx])
				out.WriteString(closeTag)
				pos += endIdx + len(closeTag)
			}
		}
	}

	return out.String()
}

// EscapeUnsupportedTags replaces any HTML-like tag whose name is not in
// Telegram's supported set with its entity-escaped equivalent so Telegram
// doesn't reject the message with a parse error.
func EscapeUnsupportedTags(s string) string {
	return htmlTagRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := htmlTagRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		// sub[1] is the tag name, with an optional leading "/" for closing tags.
		tagName := strings.ToLower(strings.TrimPrefix(sub[1], "/"))
		if supportedTelegramTags[tagName] {
			return match
		}
		return strings.ReplaceAll(strings.ReplaceAll(match, "<", "&lt;"), ">", "&gt;")
	})
}

// MarkdownToHTML converts common markdown patterns the model may
// produce into Telegram-compatible HTML. This is a best-effort fallback —
// the system prompt asks for HTML, but models sometimes slip into markdown.
// It also escapes any HTML-like tags that Telegram doesn't support so they
// don't cause parse errors.
func MarkdownToHTML(s string) string {
	// Always convert Markdown links — [text](url) does not overlap with HTML
	// tags, so it is safe to convert unconditionally even in mixed responses.
	s = markdownLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := markdownLink.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		return `<a href="` + sub[2] + `">` + sub[1] + `</a>`
	})

	// Only convert the remaining Markdown patterns if the model hasn't already
	// used HTML — avoids double-converting responses that correctly use HTML.
	if !strings.Contains(s, "<b>") && !strings.Contains(s, "<code>") && !strings.Contains(s, "<pre>") {
		s = markdownHeader.ReplaceAllString(s, "<b>$1</b>")
		s = markdownBold.ReplaceAllString(s, "<b>$1</b>")
		s = markdownInlineCode.ReplaceAllString(s, "<code>$1</code>")
		s = markdownBullet.ReplaceAllString(s, "• ")
	}

	// Always escape unsupported tags — the model may include path-like strings
	// such as <userDir> that Telegram's HTML parser rejects.
	return EscapeUnsupportedTags(s)
}

// StripAllTags removes every HTML tag from s, preserving only text content.
// Used as a last-resort fallback when Telegram rejects the formatted message.
func StripAllTags(s string) string {
	return htmlTagPattern.ReplaceAllString(s, "")
}

// SanitizeUTF8 drops bytes that are not valid UTF-8. Telegram rejects messages
// containing invalid byte sequences with a 400 error that will never succeed
// on retry — dropping the offending bytes is preferable to losing the whole message.
func SanitizeUTF8(s string) string {
	return strings.ToValidUTF8(s, "")
}

// TruncateSnippet returns the first maxLen characters of s, appending "…"
// if truncated. Newlines are collapsed to spaces for a compact single-line preview.
func TruncateSnippet(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= maxLen {
		return s
	}
	return string([]rune(s)[:maxLen]) + "…"
}
