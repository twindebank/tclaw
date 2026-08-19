package markdownpdf_test

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/markdownpdf"
)

func TestParse(t *testing.T) {
	t.Run("headings carry their level", func(t *testing.T) {
		blocks := markdownpdf.Parse("# Title\n\n### Third\n")

		require.Len(t, blocks, 2, "expected one block per heading")
		require.Equal(t, markdownpdf.BlockHeading, blocks[0].Kind)
		require.Equal(t, 1, blocks[0].Level, "first heading level")
		require.Equal(t, "Title", blocks[0].Runs[0].Text)
		require.Equal(t, 3, blocks[1].Level, "second heading level")
	})

	t.Run("wrapped lines join into one paragraph", func(t *testing.T) {
		blocks := markdownpdf.Parse("The latch closes behind you.\nTake the keys every time.\n\nNext one.")

		require.Len(t, blocks, 2, "two blank-line separated paragraphs")
		require.Equal(t, markdownpdf.BlockParagraph, blocks[0].Kind)
		require.Equal(t, "The latch closes behind you. Take the keys every time.", runText(blocks[0].Runs))
		require.Equal(t, "Next one.", runText(blocks[1].Runs))
	})

	t.Run("a paragraph stops at the next block", func(t *testing.T) {
		blocks := markdownpdf.Parse("Some text\n- an item\n")

		require.Len(t, blocks, 2, "paragraph then list")
		require.Equal(t, markdownpdf.BlockParagraph, blocks[0].Kind)
		require.Equal(t, "Some text", runText(blocks[0].Runs))
		require.Equal(t, markdownpdf.BlockList, blocks[1].Kind)
	})

	t.Run("nested and numbered list items keep depth and numbering", func(t *testing.T) {
		blocks := markdownpdf.Parse("- top\n  - nested\n- back\n\n1. first\n1. second\n")

		require.Len(t, blocks, 2, "bullet list then numbered list")
		bullets := blocks[0].Items
		require.Len(t, bullets, 3, "three bullet items")
		require.Equal(t, 1, bullets[0].Depth, "top level depth")
		require.Equal(t, 2, bullets[1].Depth, "nested depth")
		require.False(t, bullets[0].Ordered, "bullets are not ordered")

		numbers := blocks[1].Items
		require.Len(t, numbers, 2, "two numbered items")
		require.True(t, numbers[0].Ordered, "numbered items are ordered")
		require.Equal(t, 1, numbers[0].Number, "first item numbers from one")
		require.Equal(t, 2, numbers[1].Number, "repeated 1. still counts up")
	})

	t.Run("a table with a separator row keeps its header", func(t *testing.T) {
		blocks := markdownpdf.Parse("| Room | How |\n| --- | --- |\n| Lounge | Motion |\n")

		require.Len(t, blocks, 1, "one table block")
		require.Equal(t, markdownpdf.BlockTable, blocks[0].Kind)
		require.Equal(t, []string{"Room", "How"}, cellText(blocks[0].Header))
		require.Equal(t, []string{"Lounge", "Motion"}, cellText(blocks[0].Rows[0]))
		require.Len(t, blocks[0].Rows, 1, "one data row")
	})

	t.Run("a table without a separator row still parses", func(t *testing.T) {
		blocks := markdownpdf.Parse("| Say | Effect |\n| turn TV on | telly |\n")

		require.Len(t, blocks, 1, "one table block")
		require.Equal(t, []string{"Say", "Effect"}, cellText(blocks[0].Header))
		require.Equal(t, []string{"turn TV on", "telly"}, cellText(blocks[0].Rows[0]))
	})

	t.Run("a warning sign makes a callout whichever way it is written", func(t *testing.T) {
		blocks := markdownpdf.Parse("⚠️ Take the keys.\n\n> Ordinary note.\n")

		require.Len(t, blocks, 2, "two callouts")
		require.Equal(t, markdownpdf.BlockCallout, blocks[0].Kind)
		require.True(t, blocks[0].Warning, "warning sign marks the callout")
		require.Equal(t, "Take the keys.", runText(blocks[0].Runs), "the sign itself is stripped")
		require.False(t, blocks[1].Warning, "a plain quote is not a warning")
		require.Equal(t, "Ordinary note.", runText(blocks[1].Runs))
	})

	t.Run("an image on its own line becomes an image block", func(t *testing.T) {
		blocks := markdownpdf.Parse("![Dayo and Tato](cats.jpg)\n")

		require.Len(t, blocks, 1, "one image block")
		require.Equal(t, markdownpdf.BlockImage, blocks[0].Kind)
		require.Equal(t, "cats.jpg", blocks[0].Source)
		require.Equal(t, "Dayo and Tato", blocks[0].Alt)
	})

	t.Run("inline marks split into runs", func(t *testing.T) {
		blocks := markdownpdf.Parse("Use **both** the `feed` button and [the app](https://example.com).")

		runs := blocks[0].Runs
		require.Equal(t, "Use both the feed button and the app.", runText(runs))

		marks := map[string]markdownpdf.Run{}
		for _, run := range runs {
			marks[run.Text] = run
		}
		require.True(t, marks["both"].Bold, "double stars are bold")
		require.True(t, marks["feed"].Code, "backticks are code")
		require.Equal(t, "https://example.com", marks["the app"].Link, "link target is kept")
	})

	t.Run("an unclosed code fence keeps its body", func(t *testing.T) {
		blocks := markdownpdf.Parse("```\nline one\nline two\n")

		require.Len(t, blocks, 1, "one code block")
		require.Equal(t, markdownpdf.BlockCode, blocks[0].Kind)
		require.Equal(t, "line one\nline two", blocks[0].Text)
	})
}

func TestCredentialKeys(t *testing.T) {
	t.Run("returns each key once, in the order it first appears", func(t *testing.T) {
		keys := markdownpdf.CredentialKeys("${cred:wifi} then ${cred:door} then ${cred:wifi} again")

		require.Equal(t, []string{"wifi", "door"}, keys)
	})

	t.Run("returns nothing for a document with no placeholders", func(t *testing.T) {
		require.Empty(t, markdownpdf.CredentialKeys("# Guide\n\nNothing secret here.\n"))
	})
}

func TestRender(t *testing.T) {
	t.Run("produces a pdf covering every block kind", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown: "# Guide\n\nA lede line.\n\n## Section\n\n- one\n- two\n\n" +
				"| A | B |\n| --- | --- |\n| 1 | 2 |\n\n> A note.\n\n⚠️ A warning.\n\n---\n\n" +
				"```\ncode here\n```\n",
			Title: "Guide",
		})

		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(out, []byte("%PDF-")), "output should be a PDF")
		require.Greater(t, len(out), 1000, "a document with this much content should not be near-empty")
	})

	t.Run("renders an image from the assets directory", func(t *testing.T) {
		dir := t.TempDir()
		writeTestPNG(t, filepath.Join(dir, "cats.png"))

		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:  "# Guide\n\n![Dayo and Tato](cats.png)\n",
			AssetsDir: dir,
		})

		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(out, []byte("%PDF-")), "output should be a PDF")
	})

	t.Run("rejects empty markdown", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{Markdown: "   \n"})

		require.Error(t, err)
		require.Equal(t, "markdown is empty", err.Error())
	})

	t.Run("rejects a character the font cannot express", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{Markdown: "# Feeding 🐈\n"})

		require.Error(t, err)
		require.Equal(t, `cannot render these characters: '🐈' (U+1F408)`, err.Error())
	})

	t.Run("accepts the punctuation the guides actually use", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown: "# Heating\n\nDry and 20–22° all weekend — worth sitting out · genuinely.\n",
		})

		require.NoError(t, err, "en dash, em dash, degree and middle dot must all render")
		require.True(t, bytes.HasPrefix(out, []byte("%PDF-")), "output should be a PDF")
	})

	t.Run("a warning sign renders wherever it is written", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown: "# Cats\n\n- ⚠️ Take the keys every time\n\n| ⚠️ Watch | Why |\n| --- | --- |\n| the latch | it closes behind you |\n",
		})

		require.NoError(t, err, "a warning sign in a list item or table cell must not fail the render")
		require.True(t, bytes.HasPrefix(out, []byte("%PDF-")), "output should be a PDF")
	})

	t.Run("a credential's punctuation stays literal in a paragraph", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "# WiFi\n\nPassword: ${cred:wifi}\n",
			Credentials: map[string]string{"wifi": "a*b*c"},
		})

		require.NoError(t, err)
		require.Contains(t, strings.Join(pdfText(t, out), "\n"), "a*b*c",
			"the asterisks must reach the page, not be eaten as italic markers")
	})

	t.Run("a credential's punctuation stays literal in a table cell", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "| What | Value |\n| --- | --- |\n| WiFi | ${cred:wifi} |\n",
			Credentials: map[string]string{"wifi": "a*b*c"},
		})

		require.NoError(t, err)
		require.Contains(t, strings.Join(pdfText(t, out), "\n"), "a*b*c",
			"a credentials table is the natural layout, so a cell must not re-parse the value")
	})

	t.Run("a credential containing a pipe does not split a table cell", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "| What | Value |\n| --- | --- |\n| WiFi | ${cred:wifi} |\n",
			Credentials: map[string]string{"wifi": "a|b"},
		})

		require.NoError(t, err)
		require.Contains(t, strings.Join(pdfText(t, out), "\n"), "a|b",
			"a pipe in a value must not become a column break")
	})

	t.Run("refuses a credential placed in a link target", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "Tap [here](https://example.com/?q=${cred:wifi}) to join.\n",
			Credentials: map[string]string{"wifi": "correct-horse"},
		})

		require.Error(t, err, "a value in a URL would be sent to that host on one tap")
		require.Equal(t,
			"a credential cannot go in a link target (https://example.com/?q=${cred:wifi}) — put it in the text",
			err.Error())
	})

	t.Run("a credential never reaches a link target even when one is nearby", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "Password: ${cred:wifi}. See [the site](https://example.com/).\n",
			Credentials: map[string]string{"wifi": "correct-horse"},
		})

		require.NoError(t, err)
		require.NotContains(t, string(out), "example.com/correct-horse", "no value in a URL")
		require.Contains(t, strings.Join(pdfText(t, out), "\n"), "correct-horse", "but it is in the text")
	})

	t.Run("refuses a mistyped placeholder rather than printing it", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{Markdown: "Password: ${cred:WiFi}\n"})

		require.Error(t, err, "a delivered document must never show a raw placeholder")
		require.Equal(t,
			"these placeholder keys are not valid (lower case, digits and underscores only): WiFi",
			err.Error())
	})

	t.Run("names the key, never the characters, when a credential cannot be rendered", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "# WiFi\n\nPassword: ${cred:wifi}\n",
			Credentials: map[string]string{"wifi": "pass🐈word"},
		})

		require.Error(t, err)
		require.Equal(t, `the value set for "wifi" contains a character that cannot be rendered into a PDF`, err.Error())
		require.NotContains(t, err.Error(), "🐈", "the error must not leak any part of the value")
	})

	t.Run("refuses an image reached through a symlink out of the assets directory", func(t *testing.T) {
		outside := t.TempDir()
		writeTestPNG(t, filepath.Join(outside, "secret.png"))
		assets := t.TempDir()
		require.NoError(t, os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(assets, "innocent.png")))

		_, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:  "![borrowed](innocent.png)\n",
			AssetsDir: assets,
		})

		require.Error(t, err)
		require.Equal(t, `image path "innocent.png" must sit inside the assets directory`, err.Error())
	})

	t.Run("rejects a link target the font cannot express", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown: "See [the page](https://example.com/caf\u00e9/\U0001F408).\n",
		})

		require.Error(t, err)
		require.Equal(t, `cannot render these characters: '🐈' (U+1F408)`, err.Error())
	})

	t.Run("rejects an image when no assets directory is given", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{Markdown: "![cats](cats.png)\n"})

		require.Error(t, err)
		require.Equal(t, "document has an image (cats.png) but no assets directory was given", err.Error())
	})

	t.Run("refuses an image path outside the assets directory", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:  "![escape](../../etc/passwd)\n",
			AssetsDir: t.TempDir(),
		})

		require.Error(t, err)
		require.Equal(t, `image path "../../etc/passwd" must sit inside the assets directory`, err.Error())
	})

	t.Run("reports a missing image rather than skipping it", func(t *testing.T) {
		_, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:  "![gone](missing.png)\n",
			AssetsDir: t.TempDir(),
		})

		require.Error(t, err)
		require.Contains(t, err.Error(), `image "missing.png"`, "the error names the image")
	})
}

// --- helpers ---

// cellText flattens a parsed table row back to plain strings for assertions.
func cellText(cells []([]markdownpdf.Run)) []string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = runText(cell)
	}
	return out
}

func runText(runs []markdownpdf.Run) string {
	var joined string
	for _, run := range runs {
		joined += run.Text
	}
	return joined
}

// pdfText inflates the PDF's content streams and returns the text it shows,
// one entry per drawn run. Raw PDF bytes are not comparable: fpdf emits font
// objects in map order and stamps timestamps, so two identical renders differ.
func pdfText(t *testing.T, pdf []byte) []string {
	t.Helper()
	var shown []string
	for _, stream := range regexp.MustCompile(`(?s)stream\r?\n(.*?)endstream`).FindAllSubmatch(pdf, -1) {
		reader, err := zlib.NewReader(bytes.NewReader(stream[1]))
		if err != nil {
			continue
		}
		body, err := io.ReadAll(reader)
		require.NoError(t, reader.Close())
		if err != nil {
			continue
		}
		for _, run := range regexp.MustCompile(`\(((?:[^()\\]|\\.)*)\)\s*Tj`).FindAllSubmatch(body, -1) {
			text := string(run[1])
			for _, pair := range [][2]string{{`\(`, "("}, {`\)`, ")"}, {`\\`, `\`}} {
				text = strings.ReplaceAll(text, pair[0], pair[1])
			}
			shown = append(shown, text)
		}
	}
	require.NotEmpty(t, shown, "no text found in the PDF")
	return shown
}

func pageCount(t *testing.T, pdf []byte) int {
	t.Helper()
	return len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(pdf, -1))
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			img.Set(x, y, color.RGBA{R: 200, G: 120, B: 60, A: 255})
		}
	}
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, file.Close())
}

func TestRender_LongBlocks(t *testing.T) {
	words := strings.TrimSpace(strings.Repeat("alpha beta gamma delta epsilon ", 600))

	t.Run("a callout longer than a page does not emit a page per line", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{Markdown: "> " + words + "\n"})

		require.NoError(t, err)
		require.LessOrEqual(t, pageCount(t, out), 12,
			"a long callout should flow across a few pages, not one per line")
	})

	t.Run("a table row longer than a page does not emit a page per line", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown: "| A | B |\n| --- | --- |\n| short | " + words + " |\n",
		})

		require.NoError(t, err)
		require.LessOrEqual(t, pageCount(t, out), 12,
			"a long table row should flow across a few pages, not one per line")
	})

	t.Run("the same text as a paragraph sets the baseline", func(t *testing.T) {
		out, err := markdownpdf.Render(markdownpdf.RenderParams{Markdown: words + "\n"})

		require.NoError(t, err)
		require.LessOrEqual(t, pageCount(t, out), 12, "baseline for the two above")
	})
}

func TestParse_LooseLists(t *testing.T) {
	t.Run("blank lines between numbered items keep the numbering", func(t *testing.T) {
		blocks := markdownpdf.Parse("1. first\n\n2. second\n\n3. third\n")

		require.Len(t, blocks, 1, "one list, not three")
		require.Len(t, blocks[0].Items, 3, "three items")
		require.Equal(t, []int{1, 2, 3}, []int{
			blocks[0].Items[0].Number, blocks[0].Items[1].Number, blocks[0].Items[2].Number,
		}, "a blank line between items must not restart the count")
	})

	t.Run("a blank line before a different kind of list starts a new one", func(t *testing.T) {
		blocks := markdownpdf.Parse("- a bullet\n\n1. a number\n")

		require.Len(t, blocks, 2, "bullet list then numbered list")
		require.False(t, blocks[0].Items[0].Ordered)
		require.True(t, blocks[1].Items[0].Ordered)
	})
}
