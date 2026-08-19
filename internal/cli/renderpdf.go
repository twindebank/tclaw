package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tclaw/internal/libraries/markdownpdf"
)

const renderPDFUsage = `tclaw render-pdf — turn a markdown file into a PDF

Usage: tclaw render-pdf <input.md> <output.pdf> [flags]

Flags:
  --title string    PDF metadata title (defaults to the input filename)
  --assets string   Directory relative image paths resolve against (defaults to the input's directory)

Supported markdown: headings, paragraphs, bullet and numbered lists, tables, blockquotes,
fenced code, horizontal rules, images on their own line, and inline bold, italic, code and links.
A paragraph or blockquote opening with a warning sign renders as a red callout; every other
blockquote renders amber.
`

func runRenderPDF() {
	flags := flag.NewFlagSet("render-pdf", flag.ExitOnError)
	title := flags.String("title", "", "PDF metadata title")
	assets := flags.String("assets", "", "directory relative image paths resolve against")
	flags.Usage = func() { fmt.Fprint(os.Stderr, renderPDFUsage) }

	// flag stops at the first positional argument, so the two paths are split out first
	split := splitFlagArgs(os.Args[2:], map[string]bool{"title": true, "assets": true})
	if err := flags.Parse(split.Flags); err != nil {
		os.Exit(1)
	}
	if len(split.Positional) != 2 {
		fmt.Fprint(os.Stderr, renderPDFUsage)
		os.Exit(1)
	}

	source, destination := split.Positional[0], split.Positional[1]
	markdown, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", source, err)
		os.Exit(1)
	}

	assetsDir := *assets
	if assetsDir == "" {
		assetsDir = filepath.Dir(source)
	}
	documentTitle := *title
	if documentTitle == "" {
		documentTitle = filepath.Base(source)
	}

	if keys := markdownpdf.CredentialKeys(string(markdown)); len(keys) > 0 {
		fmt.Fprintf(os.Stderr,
			"⚠️  %s has credential placeholders (%s) and this command cannot fill them — they will print literally.\n"+
				"    Resolve them first, or render through the document_send_pdf tool.\n",
			source, strings.Join(keys, ", "))
	}

	pdf, err := markdownpdf.Render(markdownpdf.RenderParams{
		Markdown:  string(markdown),
		Title:     documentTitle,
		AssetsDir: assetsDir,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "render %s: %v\n", source, err)
		os.Exit(1)
	}

	if err := os.WriteFile(destination, pdf, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", destination, err)
		os.Exit(1)
	}
	fmt.Printf("📄 wrote %s (%d bytes)\n", destination, len(pdf))
}

// splitArgs separates a command tail into flags and positional arguments so
// they can be given in any order.
type splitArgs struct {
	Flags      []string
	Positional []string
}

// takesValue names the flags whose value is a separate argument, so
// "--title My Guide" is not mistaken for a positional.
func splitFlagArgs(args []string, takesValue map[string]bool) splitArgs {
	var split splitArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			split.Positional = append(split.Positional, arg)
			continue
		}
		split.Flags = append(split.Flags, arg)
		name := strings.TrimLeft(arg, "-")
		if _, _, found := strings.Cut(name, "="); found {
			continue
		}
		if takesValue[name] && i+1 < len(args) {
			i++
			split.Flags = append(split.Flags, args[i])
		}
	}
	return split
}
