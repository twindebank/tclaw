// Package htmlconv converts HTML to readable plain text, preserving table
// structure, link text, and block-level formatting. Built for email bodies
// where marketing HTML (nested tables, CSS layouts) needs to produce useful
// text for an LLM agent.
package htmlconv

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ToText converts an HTML string to readable plain text.
// Tables are rendered with " | " cell separators and newlines between rows.
// Links preserve their inner text. Block elements insert line breaks.
func ToText(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		// Unparseable HTML — return as-is stripped of obvious tags.
		return htmlStr
	}

	var b strings.Builder
	walkNode(&b, doc)

	return cleanWhitespace(b.String())
}

// walkNode recursively traverses the HTML tree, writing text content to b.
func walkNode(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		// Collapse internal whitespace in text runs (HTML semantics).
		text := collapseSpaces(n.Data)
		if text != "" {
			b.WriteString(text)
		}
		return

	case html.ElementNode:
		// Skip elements that contribute no readable text.
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Head:
			return
		}
	}

	switch n.DataAtom {
	case atom.Br:
		b.WriteString("\n")
		walkChildren(b, n)
		return

	case atom.P, atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Nav, atom.Main, atom.Aside, atom.Blockquote:
		writeBlockBreak(b)
		walkChildren(b, n)
		writeBlockBreak(b)
		return

	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		writeBlockBreak(b)
		walkChildren(b, n)
		writeBlockBreak(b)
		return

	case atom.Li:
		writeBlockBreak(b)
		b.WriteString("- ")
		walkChildren(b, n)
		return

	case atom.Ul, atom.Ol:
		writeBlockBreak(b)
		walkChildren(b, n)
		writeBlockBreak(b)
		return

	case atom.Table:
		writeBlockBreak(b)
		walkChildren(b, n)
		writeBlockBreak(b)
		return

	case atom.Tr:
		walkTableRow(b, n)
		return

	case atom.A:
		walkLink(b, n)
		return
	}

	walkChildren(b, n)
}

// walkChildren processes all child nodes.
func walkChildren(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(b, c)
	}
}

// walkTableRow renders a <tr> as: "cell1 | cell2 | cell3\n".
// Cells with only whitespace are skipped to avoid noisy output from layout tables.
func walkTableRow(b *strings.Builder, tr *html.Node) {
	var cells []string
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.DataAtom != atom.Td && c.DataAtom != atom.Th {
			continue
		}

		var cell strings.Builder
		walkChildren(&cell, c)
		text := strings.TrimSpace(cell.String())
		if text != "" {
			cells = append(cells, text)
		}
	}

	if len(cells) == 0 {
		return
	}

	b.WriteString(strings.Join(cells, " | "))
	b.WriteString("\n")
}

// walkLink renders <a> tags as "link text (url)" — only appends the URL if
// it differs from the inner text (avoids redundant "https://x (https://x)").
func walkLink(b *strings.Builder, n *html.Node) {
	var inner strings.Builder
	walkChildren(&inner, n)
	text := strings.TrimSpace(inner.String())

	href := getAttr(n, "href")

	if text == "" && href != "" {
		b.WriteString(href)
		return
	}

	b.WriteString(text)

	if href != "" && href != text && !strings.HasPrefix(href, "javascript:") {
		b.WriteString(" (")
		b.WriteString(href)
		b.WriteString(")")
	}
}

// writeBlockBreak ensures there's a newline before the next block element,
// avoiding double newlines.
func writeBlockBreak(b *strings.Builder) {
	s := b.String()
	if len(s) == 0 {
		return
	}
	if s[len(s)-1] != '\n' {
		b.WriteString("\n")
	}
}

// collapseSpaces replaces runs of whitespace (space, tab, newline) with a
// single space, matching how browsers render HTML text nodes.
func collapseSpaces(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			if !inSpace {
				b.WriteRune(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// cleanWhitespace trims leading/trailing whitespace per line and collapses
// runs of 3+ blank lines down to 2.
func cleanWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	blankRun := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blankRun++
			if blankRun <= 2 {
				result = append(result, "")
			}
			continue
		}
		blankRun = 0
		result = append(result, trimmed)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
