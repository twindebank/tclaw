package markdownpdf_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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
		require.Equal(t, []string{"Room", "How"}, blocks[0].Header)
		require.Equal(t, [][]string{{"Lounge", "Motion"}}, blocks[0].Rows)
	})

	t.Run("a table without a separator row still parses", func(t *testing.T) {
		blocks := markdownpdf.Parse("| Say | Effect |\n| turn TV on | telly |\n")

		require.Len(t, blocks, 1, "one table block")
		require.Equal(t, []string{"Say", "Effect"}, blocks[0].Header)
		require.Equal(t, [][]string{{"turn TV on", "telly"}}, blocks[0].Rows)
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
		blocks := markdownpdf.Parse("![Mabel and Otto](cats.jpg)\n")

		require.Len(t, blocks, 1, "one image block")
		require.Equal(t, markdownpdf.BlockImage, blocks[0].Kind)
		require.Equal(t, "cats.jpg", blocks[0].Source)
		require.Equal(t, "Mabel and Otto", blocks[0].Alt)
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
			Markdown:  "# Guide\n\n![Mabel and Otto](cats.png)\n",
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

	t.Run("a credential is substituted after parsing, so its punctuation is literal", func(t *testing.T) {
		const value = "a*b*c`d`"

		// substituted through a placeholder, the asterisks and backticks are text
		viaPlaceholder, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    "# WiFi\n\nPassword: ${cred:wifi}\n",
			Credentials: map[string]string{"wifi": value},
		})
		require.NoError(t, err)

		// written into the markdown itself, the same characters are markup
		asMarkup, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown: "# WiFi\n\nPassword: " + value + "\n",
		})
		require.NoError(t, err)

		require.NotEqual(t, asMarkup, viaPlaceholder,
			"a value substituted before parsing would render identically to the same characters written as markup")
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

func runText(runs []markdownpdf.Run) string {
	var joined string
	for _, run := range runs {
		joined += run.Text
	}
	return joined
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
