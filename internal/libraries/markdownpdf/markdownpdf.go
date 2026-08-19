package markdownpdf

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/encoding/charmap"
)

const (
	warningSign = "⚠"
	// variationSelector follows the warning sign when it is typed as an emoji.
	variationSelector = "️"
)

// RenderParams describes one document to render.
type RenderParams struct {
	Markdown string

	// Title is written into the PDF metadata, not onto the page. The document's
	// own first heading is what a reader sees.
	Title string

	// AssetsDir is where relative image paths resolve. Images are skipped when empty.
	AssetsDir string

	// Credentials fill ${cred:key} placeholders. Substitution happens after the
	// markdown is parsed, so punctuation in a value cannot change the document.
	Credentials map[string]string
}

// Render turns markdown into a PDF. It fails rather than substituting when the
// text carries a character the PDF's Windows-1252 font cannot express.
func Render(p RenderParams) ([]byte, error) {
	if strings.TrimSpace(p.Markdown) == "" {
		return nil, fmt.Errorf("markdown is empty")
	}

	// the sign is prose styling: it marks a callout and is then removed, so one
	// written inside a list item or a table cell does not fail the render
	blocks := mapProseText(Parse(p.Markdown), stripWarningSign)
	if unsupported := unencodableRunes(blocks); len(unsupported) > 0 {
		return nil, fmt.Errorf("cannot render these characters: %s", strings.Join(unsupported, ", "))
	}
	if bad := malformedCredentialKeys(p.Markdown); len(bad) > 0 {
		return nil, fmt.Errorf("these placeholder keys are not valid (lower case, digits and underscores only): %s",
			strings.Join(bad, ", "))
	}

	// substituted last so a value containing *, ` or | cannot restructure the
	// document, and so a bad value names its key rather than its characters
	filled, err := substituteCredentials(blocks, p.Credentials)
	if err != nil {
		return nil, err
	}

	return renderBlocks(filled, p)
}

// substituteCredentials fills placeholders in already-parsed text. A value that
// the font cannot express names its key, never the characters it contains.
func substituteCredentials(blocks []Block, credentials map[string]string) ([]Block, error) {
	if len(credentials) == 0 {
		return blocks, nil
	}

	for _, block := range blocks {
		for _, run := range allRuns(block) {
			// a value in a URL is sent to whoever owns that host on one tap
			if credentialRef.MatchString(run.Link) {
				return nil, fmt.Errorf("a credential cannot go in a link target (%s) — put it in the text", run.Link)
			}
		}
	}

	encoder := charmap.Windows1252.NewEncoder()
	for key, value := range credentials {
		if _, err := encoder.String(value); err != nil {
			return nil, fmt.Errorf("the value set for %q contains a character that cannot be rendered into a PDF", key)
		}
	}

	fill := func(text string) string {
		if !strings.Contains(text, "${cred:") {
			return text
		}
		return credentialRef.ReplaceAllStringFunc(text, func(ref string) string {
			key := credentialRef.FindStringSubmatch(ref)[1]
			if value, found := credentials[key]; found {
				return value
			}
			return ref
		})
	}

	return mapProseText(blocks, fill), nil
}

// credentialRef matches a placeholder filled from RenderParams.Credentials.
var credentialRef = regexp.MustCompile(`\$\{cred:([a-z0-9_]+)\}`)

// anyCredentialRef matches a placeholder whatever its key, so a mistyped one is
// an error rather than a literal ${cred:...} in the delivered document.
var anyCredentialRef = regexp.MustCompile(`\$\{cred:([^}]*)\}`)

// CredentialKeys returns the placeholder keys a document references, in the
// order they first appear.
func CredentialKeys(markdown string) []string {
	var keys []string
	seen := map[string]bool{}
	for _, match := range credentialRef.FindAllStringSubmatch(markdown, -1) {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		keys = append(keys, match[1])
	}
	return keys
}

// malformedCredentialKeys returns placeholder keys that would never resolve.
func malformedCredentialKeys(markdown string) []string {
	var bad []string
	seen := map[string]bool{}
	for _, match := range anyCredentialRef.FindAllStringSubmatch(markdown, -1) {
		if credentialRef.MatchString(match[0]) || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		bad = append(bad, match[1])
	}
	sort.Strings(bad)
	return bad
}

// mapProseText passes only the prose through transform, leaving code bodies and
// link targets untouched.
func mapProseText(blocks []Block, transform func(string) string) []Block {
	return mapText(blocks, transform, false)
}

// mapBlockText passes every piece of text through transform, code and link
// targets included.
func mapBlockText(blocks []Block, transform func(string) string) []Block {
	return mapText(blocks, transform, true)
}

func mapText(blocks []Block, transform func(string) string, includeVerbatim bool) []Block {
	mapRuns := func(runs []Run) []Run {
		out := make([]Run, len(runs))
		for i, run := range runs {
			out[i] = run
			out[i].Text = transform(run.Text)
			if includeVerbatim {
				out[i].Link = transform(run.Link)
			}
		}
		return out
	}
	mapCells := func(cells []([]Run)) []([]Run) {
		out := make([]([]Run), len(cells))
		for i, cell := range cells {
			out[i] = mapRuns(cell)
		}
		return out
	}

	out := make([]Block, len(blocks))
	for i, block := range blocks {
		out[i] = block
		out[i].Runs = mapRuns(block.Runs)
		out[i].Alt = transform(block.Alt)
		if includeVerbatim {
			out[i].Text = transform(block.Text)
		}
		out[i].Header = mapCells(block.Header)

		out[i].Items = make([]ListItem, len(block.Items))
		for j, item := range block.Items {
			out[i].Items[j] = item
			out[i].Items[j].Runs = mapRuns(item.Runs)
		}

		out[i].Rows = make([][]([]Run), len(block.Rows))
		for j, row := range block.Rows {
			out[i].Rows[j] = mapCells(row)
		}
	}
	return out
}

// allRuns returns every run a block carries, whichever field holds them.
func allRuns(block Block) []Run {
	runs := append([]Run(nil), block.Runs...)
	for _, item := range block.Items {
		runs = append(runs, item.Runs...)
	}
	for _, cell := range block.Header {
		runs = append(runs, cell...)
	}
	for _, row := range block.Rows {
		for _, cell := range row {
			runs = append(runs, cell...)
		}
	}
	return runs
}

// stripWarningSign removes the sign and its variation selector wherever they
// appear, and tidies the space a leading one leaves behind.
func stripWarningSign(text string) string {
	if !strings.Contains(text, warningSign) {
		return text
	}
	stripped := strings.ReplaceAll(text, warningSign, "")
	stripped = strings.ReplaceAll(stripped, variationSelector, "")
	return strings.TrimSpace(stripped)
}

// unencodableRunes returns a sorted, human-readable description of every rune
// the document uses that Windows-1252 has no code point for.
func unencodableRunes(blocks []Block) []string {
	encoder := charmap.Windows1252.NewEncoder()
	found := map[rune]bool{}

	var check func(string)
	check = func(text string) {
		for _, r := range text {
			if r == '\n' || r == '\t' || found[r] {
				continue
			}
			if _, err := encoder.String(string(r)); err != nil {
				found[r] = true
			}
		}
	}
	checkRuns := func(runs []Run) {
		for _, run := range runs {
			check(run.Text)
			check(run.Link)
		}
	}

	for _, block := range blocks {
		checkRuns(block.Runs)
		check(block.Text)
		check(block.Alt)
		for _, item := range block.Items {
			checkRuns(item.Runs)
		}
		for _, cell := range block.Header {
			checkRuns(cell)
		}
		for _, row := range block.Rows {
			for _, cell := range row {
				checkRuns(cell)
			}
		}
	}

	descriptions := make([]string, 0, len(found))
	for r := range found {
		descriptions = append(descriptions, describeRune(r))
	}
	sort.Strings(descriptions)
	return descriptions
}

func describeRune(r rune) string {
	if unicode.IsPrint(r) {
		return fmt.Sprintf("%q (U+%04X)", r, r)
	}
	return fmt.Sprintf("U+%04X", r)
}
