package markdownpdf

import (
	"regexp"
	"strconv"
	"strings"
)

// BlockKind identifies which of Block's fields carry the block's content.
type BlockKind string

const (
	BlockHeading   BlockKind = "heading"
	BlockParagraph BlockKind = "paragraph"
	BlockCallout   BlockKind = "callout"
	BlockList      BlockKind = "list"
	BlockTable     BlockKind = "table"
	BlockImage     BlockKind = "image"
	BlockRule      BlockKind = "rule"
	BlockCode      BlockKind = "code"
)

// Block is one parsed markdown block. Kind says which fields apply.
type Block struct {
	Kind BlockKind

	// Level is the heading depth, 1 to 4.
	Level int

	// Runs carries the inline content of a heading, paragraph or callout.
	Runs []Run

	// Warning marks a callout that opened with the warning sign.
	Warning bool

	Items []ListItem

	// Header is the first row, whether or not a separator row followed it.
	Header []([]Run)
	Rows   [][]([]Run)

	// Source is an image path, relative to the assets directory.
	Source string
	// Alt becomes the caption printed under an image.
	Alt string

	// Text is the body of a code block, newlines included.
	Text string
}

// ListItem is one entry in a list, at a nesting depth starting from 1.
type ListItem struct {
	Depth   int
	Ordered bool
	Number  int
	Runs    []Run
}

// Run is a span of inline text sharing one set of marks.
type Run struct {
	Text   string
	Bold   bool
	Italic bool
	Code   bool
	Link   string
}

var (
	headingPattern = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	bulletPattern  = regexp.MustCompile(`^(\s*)([-*]|\d+\.)\s+(.*)$`)
	imagePattern   = regexp.MustCompile(`^!\[([^\]]*)\]\(([^)]+)\)$`)
	rulePattern    = regexp.MustCompile(`^-{3,}$`)
	separatorRow   = regexp.MustCompile(`^\|[\s:|-]+\|$`)
	codeFence      = regexp.MustCompile("^```")
)

// Parse turns markdown into blocks. Unrecognised syntax falls through as
// paragraph text rather than being dropped.
func Parse(markdown string) []Block {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var blocks []Block

	for i := 0; i < len(lines); {
		raw := lines[i]
		line := strings.TrimSpace(raw)

		if line == "" {
			i++
			continue
		}

		if codeFence.MatchString(line) {
			var body []string
			i++
			for i < len(lines) && !codeFence.MatchString(strings.TrimSpace(lines[i])) {
				body = append(body, lines[i])
				i++
			}
			if i < len(lines) {
				// a fence that never closes still yields its body rather than swallowing the rest
				i++
			}
			for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
				body = body[:len(body)-1]
			}
			blocks = append(blocks, Block{Kind: BlockCode, Text: strings.Join(body, "\n")})
			continue
		}

		if found := imagePattern.FindStringSubmatch(line); found != nil {
			blocks = append(blocks, Block{Kind: BlockImage, Alt: found[1], Source: found[2]})
			i++
			continue
		}

		if rulePattern.MatchString(line) {
			blocks = append(blocks, Block{Kind: BlockRule})
			i++
			continue
		}

		if found := headingPattern.FindStringSubmatch(line); found != nil {
			blocks = append(blocks, Block{
				Kind:  BlockHeading,
				Level: len(found[1]),
				Runs:  parseInline(found[2]),
			})
			i++
			continue
		}

		if strings.HasPrefix(line, ">") {
			var body []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				body = append(body, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
				i++
			}
			text := strings.TrimSpace(strings.Join(body, " "))
			warning := strings.HasPrefix(text, warningSign)
			blocks = append(blocks, Block{
				Kind:    BlockCallout,
				Warning: warning,
				Runs:    parseInline(stripWarningSign(text)),
			})
			continue
		}

		if strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") {
			table, next := parseTable(lines, i)
			blocks = append(blocks, table)
			i = next
			continue
		}

		if bulletPattern.MatchString(raw) {
			list, next := parseList(lines, i)
			blocks = append(blocks, list)
			i = next
			continue
		}

		// a paragraph runs until a blank line or the start of another block
		para := []string{line}
		i++
		for i < len(lines) {
			next := strings.TrimSpace(lines[i])
			if next == "" || startsBlock(lines[i]) {
				break
			}
			para = append(para, next)
			i++
		}
		text := strings.Join(para, " ")
		if strings.HasPrefix(text, warningSign) {
			// a callout written without the quote marker
			blocks = append(blocks, Block{Kind: BlockCallout, Warning: true, Runs: parseInline(stripWarningSign(text))})
			continue
		}
		blocks = append(blocks, Block{Kind: BlockParagraph, Runs: parseInline(text)})
	}

	return blocks
}

func startsBlock(raw string) bool {
	line := strings.TrimSpace(raw)
	switch {
	case headingPattern.MatchString(line),
		rulePattern.MatchString(line),
		imagePattern.MatchString(line),
		codeFence.MatchString(line),
		strings.HasPrefix(line, ">"),
		strings.HasPrefix(line, "|"),
		bulletPattern.MatchString(raw):
		return true
	}
	return false
}

func parseTable(lines []string, start int) (Block, int) {
	table := Block{Kind: BlockTable}
	i := start
	cells := splitRow(lines[i])

	table.Header = cells
	i++
	if i < len(lines) && separatorRow.MatchString(strings.TrimSpace(lines[i])) {
		i++
	}

	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			break
		}
		if separatorRow.MatchString(line) {
			i++
			continue
		}
		table.Rows = append(table.Rows, splitRow(lines[i]))
		i++
	}
	return table, i
}

// splitRow parses each cell's inline marks now, so a value substituted into a
// cell later cannot be re-parsed as markup.
func splitRow(raw string) []([]Run) {
	line := strings.TrimSpace(raw)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]([]Run), len(parts))
	for i, p := range parts {
		cells[i] = parseInline(strings.TrimSpace(p))
	}
	return cells
}

func parseList(lines []string, start int) (Block, int) {
	list := Block{Kind: BlockList}
	counters := map[int]int{}
	i := start

	// indent width is whatever this list uses, so two- and four-space nesting
	// both come out one level deep
	step := 0
	for j := start; j < len(lines); j++ {
		found := bulletPattern.FindStringSubmatch(lines[j])
		if found == nil {
			if strings.TrimSpace(lines[j]) == "" || startsBlock(lines[j]) {
				break
			}
			continue
		}
		if width := len(found[1]); width > 0 && (step == 0 || width < step) {
			step = width
		}
	}
	if step == 0 {
		step = 2
	}

	for i < len(lines) {
		found := bulletPattern.FindStringSubmatch(lines[i])
		if found == nil {
			// a plain line directly under an item is its continuation, not a new block
			text := strings.TrimSpace(lines[i])
			if text != "" && !startsBlock(lines[i]) && len(list.Items) > 0 {
				last := &list.Items[len(list.Items)-1]
				last.Runs = append(last.Runs, Run{Text: " "})
				last.Runs = append(last.Runs, parseInline(text)...)
				i++
				continue
			}
			break
		}

		depth := len(found[1])/step + 1
		ordered := !(found[2] == "-" || found[2] == "*")
		if ordered {
			counters[depth]++
		}
		for d := range counters {
			if d > depth {
				delete(counters, d)
			}
		}

		list.Items = append(list.Items, ListItem{
			Depth:   depth,
			Ordered: ordered,
			Number:  counters[depth],
			Runs:    parseInline(found[3]),
		})
		i++
	}
	return list, i
}

var inlinePattern = regexp.MustCompile("`[^`]+`" +
	`|\[[^\]]+\]\([^)]+\)` +
	`|\*\*[^*]+\*\*` +
	`|\*[^*\n]+\*`)

func parseInline(text string) []Run {
	var runs []Run
	last := 0
	for _, span := range inlinePattern.FindAllStringIndex(text, -1) {
		if span[0] > last {
			runs = append(runs, Run{Text: text[last:span[0]]})
		}
		runs = append(runs, markedRun(text[span[0]:span[1]]))
		last = span[1]
	}
	if last < len(text) {
		runs = append(runs, Run{Text: text[last:]})
	}
	if len(runs) == 0 {
		return []Run{{Text: text}}
	}
	return runs
}

var linkPattern = regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)$`)

func markedRun(token string) Run {
	switch {
	case strings.HasPrefix(token, "`"):
		return Run{Text: strings.Trim(token, "`"), Code: true}
	case strings.HasPrefix(token, "**"):
		return Run{Text: strings.Trim(token, "*"), Bold: true}
	case strings.HasPrefix(token, "["):
		found := linkPattern.FindStringSubmatch(token)
		return Run{Text: found[1], Link: found[2]}
	default:
		return Run{Text: strings.Trim(token, "*"), Italic: true}
	}
}

// bulletGlyph is the Windows-1252 byte for "•". Labels are generated after the
// document text has been encoded, so this one is written pre-encoded.
const bulletGlyph = "\x95"

// itemLabel is the bullet or number printed before a list item.
func itemLabel(item ListItem) string {
	if item.Ordered {
		return strconv.Itoa(item.Number) + "."
	}
	if item.Depth > 1 {
		return "-"
	}
	return bulletGlyph
}
