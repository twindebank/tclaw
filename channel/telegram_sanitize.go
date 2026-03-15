package channel

import (
	"regexp"
	"strings"
)

// telegramAllowedTags lists HTML tag names that Telegram's Bot API accepts in
// ParseMode: HTML. Everything else is stripped (text content preserved).
var telegramAllowedTags = map[string]bool{
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
var htmlTagPattern = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:\s[^>]*)?)>`)

// sanitizeTelegramHTML strips HTML tags not supported by Telegram's Bot API,
// preserving their text content. Supported tags pass through unchanged.
// Bare `<` characters that aren't part of recognized tags are escaped to &lt;.
// Content inside <pre> and <code> blocks is left untouched so code snippets
// containing angle brackets aren't mangled.
func sanitizeTelegramHTML(s string) string {
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

		if !telegramAllowedTags[tagName] {
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
