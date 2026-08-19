package markdownpdf

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-pdf/fpdf"
	"golang.org/x/text/encoding/charmap"
)

// Page geometry in millimetres. A4 with generous margins so the text column
// stays around 90 characters.
const (
	marginLeft   = 16.0
	marginTop    = 16.0
	marginRight  = 16.0
	marginBottom = 18.0
	contentWidth = 210.0 - marginLeft - marginRight

	bodySize    = 10.5
	captionSize = 8.5
	codeSize    = 9.5

	bodyLine  = 5.0
	indentPer = 5.0

	tableSize    = 9.5
	tableLine    = 4.6
	tableCellPad = 3.0

	// imageMaxHeight keeps one photo from taking a whole page.
	imageMaxHeight = 105.0
)

var (
	inkColour     = [3]int{28, 28, 30}
	mutedColour   = [3]int{108, 108, 112}
	ruleColour    = [3]int{216, 216, 220}
	noticeFill    = [3]int{255, 248, 230}
	noticeEdge    = [3]int{224, 168, 0}
	warningFill   = [3]int{253, 236, 236}
	warningEdge   = [3]int{208, 52, 44}
	codeFillShade = [3]int{242, 242, 247}
)

func renderBlocks(blocks []Block, p RenderParams) ([]byte, error) {
	encoded, err := encodeBlocks(blocks)
	if err != nil {
		return nil, fmt.Errorf("encode document text: %w", err)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(p.Title, true)
	pdf.SetMargins(marginLeft, marginTop, marginRight)
	pdf.SetAutoPageBreak(true, marginBottom)
	pdf.AddPage()
	setColour(pdf.SetTextColor, inkColour)

	// the first paragraph after the opening heading is the document's one-line summary
	ledeIndex := -1
	if len(encoded) > 1 && encoded[0].Kind == BlockHeading && encoded[1].Kind == BlockParagraph {
		ledeIndex = 1
	}

	for i, block := range encoded {
		switch block.Kind {
		case BlockHeading:
			writeHeading(pdf, block)
		case BlockParagraph:
			writeParagraph(pdf, block, i == ledeIndex)
		case BlockCallout:
			writeCallout(pdf, block)
		case BlockList:
			writeList(pdf, block)
		case BlockTable:
			writeTable(pdf, block)
		case BlockImage:
			if err := writeImage(pdf, block, p.AssetsDir); err != nil {
				return nil, err
			}
		case BlockCode:
			writeCode(pdf, block)
		case BlockRule:
			writeRule(pdf)
		default:
			return nil, fmt.Errorf("unknown block kind %q", block.Kind)
		}
	}

	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("build pdf: %w", err)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return out.Bytes(), nil
}

func writeHeading(pdf *fpdf.Fpdf, block Block) {
	switch block.Level {
	case 1:
		pdf.Ln(2)
		writeRuns(pdf, block.Runs, 9, 22, true)
		pdf.Ln(9)
		setColour(pdf.SetDrawColor, inkColour)
		pdf.SetLineWidth(0.8)
		y := pdf.GetY() + 1
		pdf.Line(marginLeft, y, marginLeft+contentWidth, y)
		pdf.Ln(5)
	case 2:
		spaceForHeading(pdf, 18)
		pdf.Ln(4)
		setColour(pdf.SetDrawColor, ruleColour)
		pdf.SetLineWidth(0.2)
		y := pdf.GetY()
		pdf.Line(marginLeft, y, marginLeft+contentWidth, y)
		pdf.Ln(3)
		writeRuns(pdf, block.Runs, 6.5, 14, true)
		pdf.Ln(7)
	default:
		spaceForHeading(pdf, 14)
		pdf.Ln(3)
		writeRuns(pdf, block.Runs, 5.5, 11.5, true)
		pdf.Ln(6)
	}
}

// linesThatFit reports how many lines of the given height still fit below the
// cursor, leaving room for a block's own padding.
func linesThatFit(pdf *fpdf.Fpdf, lineHeight, padding float64) int {
	_, pageHeight := pdf.GetPageSize()
	available := pageHeight - marginBottom - pdf.GetY() - padding
	return int(available / lineHeight)
}

// spaceForHeading pushes a heading to the next page rather than leaving it
// stranded at the bottom with its section overleaf.
func spaceForHeading(pdf *fpdf.Fpdf, needed float64) {
	_, height := pdf.GetPageSize()
	if pdf.GetY()+needed > height-marginBottom {
		pdf.AddPage()
	}
}

func writeParagraph(pdf *fpdf.Fpdf, block Block, lede bool) {
	if lede {
		setColour(pdf.SetTextColor, mutedColour)
		writeRuns(pdf, block.Runs, bodyLine+0.5, 12, false)
		setColour(pdf.SetTextColor, inkColour)
		pdf.Ln(bodyLine + 3)
		return
	}
	writeRuns(pdf, block.Runs, bodyLine, bodySize, false)
	pdf.Ln(bodyLine + 2)
}

func writeCallout(pdf *fpdf.Fpdf, block Block) {
	fill, edge := noticeFill, noticeEdge
	if block.Warning {
		fill, edge = warningFill, warningEdge
	}

	pdf.SetFont("Helvetica", "", bodySize)
	const padX, padY = 4.0, 2.5
	inner := contentWidth - 2*padX

	lines := wrapRuns(pdf, block.Runs, inner, bodySize)

	// drawn in page-sized chunks: drawing past the page break would make every
	// line after it start its own page
	for drawn := 0; drawn < len(lines); {
		fit := linesThatFit(pdf, bodyLine, 2*padY)
		if fit < 1 {
			if pdf.Error() != nil {
				// AddPage is a no-op once fpdf has failed, so this would spin
				return
			}
			pdf.AddPage()
			continue
		}
		if fit > len(lines)-drawn {
			fit = len(lines) - drawn
		}

		boxHeight := float64(fit)*bodyLine + 2*padY
		top := pdf.GetY()
		setColour(pdf.SetFillColor, fill)
		pdf.Rect(marginLeft, top, contentWidth, boxHeight, "F")
		setColour(pdf.SetFillColor, edge)
		pdf.Rect(marginLeft, top, 1.2, boxHeight, "F")

		for i, line := range lines[drawn : drawn+fit] {
			drawRunLine(pdf, line, marginLeft+padX, top+padY+float64(i)*bodyLine, bodyLine, bodySize)
		}
		pdf.SetY(top + boxHeight)
		drawn += fit
	}
	pdf.Ln(3)
}

func writeList(pdf *fpdf.Fpdf, block Block) {
	pdf.SetFont("Helvetica", "", bodySize)
	for _, item := range block.Items {
		indent := float64(item.Depth-1) * indentPer
		labelX := marginLeft + indent
		textX := labelX + 5

		pdf.SetX(labelX)
		pdf.SetFont("Helvetica", "", bodySize)
		pdf.CellFormat(5, bodyLine, itemLabel(item), "", 0, "L", false, 0, "")

		pdf.SetLeftMargin(textX)
		pdf.SetX(textX)
		writeRuns(pdf, item.Runs, bodyLine, bodySize, false)
		pdf.SetLeftMargin(marginLeft)
		pdf.Ln(bodyLine)
	}
	pdf.Ln(2)
}

func writeTable(pdf *fpdf.Fpdf, block Block) {
	columns := len(block.Header)
	for _, row := range block.Rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return
	}

	widths := columnWidths(pdf, block, columns)

	writeTableRow(pdf, block.Header, widths, true)
	for _, row := range block.Rows {
		if linesThatFit(pdf, tableLine, 2.4) < 1 {
			// the row would start on the next page, so its header goes there first
			pdf.AddPage()
			writeTableRow(pdf, block.Header, widths, true)
		}
		writeTableRow(pdf, row, widths, false)
	}
	pdf.Ln(3)
}

// columnWidths shares the content width out in proportion to how wide each
// column wants to be, but never below its longest single word, so a column
// beside a very wide one does not get squeezed until its words break.
func columnWidths(pdf *fpdf.Fpdf, block Block, columns int) []float64 {
	natural := make([]float64, columns)
	floor := make([]float64, columns)

	measure := func(cells []([]Run)) {
		for i, cell := range cells {
			if i >= columns {
				continue
			}
			// measured per run, because a code run is drawn in Courier and is
			// far wider per character than the Helvetica around it
			total, widestWord := 0.0, 0.0
			for _, run := range cell {
				applyRunFont(pdf, run, tableSize)
				total += pdf.GetStringWidth(run.Text)
				for _, word := range strings.Fields(run.Text) {
					if w := pdf.GetStringWidth(word); w > widestWord {
						widestWord = w
					}
				}
			}
			if total+tableCellPad > natural[i] {
				natural[i] = total + tableCellPad
			}
			if widestWord+tableCellPad > floor[i] {
				floor[i] = widestWord + tableCellPad
			}
		}
	}
	measure(block.Header)
	for _, row := range block.Rows {
		measure(row)
	}

	naturalTotal, floorTotal := 0.0, 0.0
	for i := range natural {
		naturalTotal += natural[i]
		floorTotal += floor[i]
	}

	widths := make([]float64, columns)
	switch {
	case naturalTotal <= contentWidth:
		for i := range widths {
			// everything fits, so the slack is shared out in proportion
			widths[i] = contentWidth * natural[i] / naturalTotal
		}
	case floorTotal >= contentWidth:
		for i := range widths {
			// even unbroken words do not fit, so split evenly
			widths[i] = contentWidth / float64(columns)
		}
	default:
		// give every column its longest word, then share what is left over
		slack := contentWidth - floorTotal
		wanted := naturalTotal - floorTotal
		for i := range widths {
			widths[i] = floor[i] + slack*(natural[i]-floor[i])/wanted
		}
	}
	return widths
}

// resolveInside turns a relative path into an absolute one under root,
// following symlinks on both sides so a link out of the tree cannot slip past.
func resolveInside(root, relative string) (string, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("image path %q must sit inside the assets directory", relative)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve assets directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, clean))
	if err != nil {
		return "", fmt.Errorf("image %q: %w", relative, err)
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("image path %q must sit inside the assets directory", relative)
	}
	return resolved, nil
}

func runText(runs []Run) string {
	var joined strings.Builder
	for _, run := range runs {
		joined.WriteString(run.Text)
	}
	return joined.String()
}

func writeTableRow(pdf *fpdf.Fpdf, cells []([]Run), widths []float64, header bool) {
	wrapped := make([][][]Run, len(widths))
	tallest := 0
	for i := range widths {
		var runs []Run
		if i < len(cells) {
			runs = cells[i]
		}
		if header {
			bold := make([]Run, len(runs))
			for j, run := range runs {
				bold[j] = run
				bold[j].Bold = true
			}
			runs = bold
		}
		wrapped[i] = wrapRuns(pdf, runs, widths[i]-tableCellPad, tableSize)
		if len(wrapped[i]) > tallest {
			tallest = len(wrapped[i])
		}
	}

	// a row taller than the page is drawn in chunks, because drawing past the
	// page break would make every line after it start its own page
	for drawn := 0; drawn < tallest; {
		fit := linesThatFit(pdf, tableLine, 2.4)
		if fit < 1 {
			if pdf.Error() != nil {
				// AddPage is a no-op once fpdf has failed, so this would spin
				return
			}
			pdf.AddPage()
			continue
		}
		if fit > tallest-drawn {
			fit = tallest - drawn
		}

		top := pdf.GetY()
		x := marginLeft
		for i := range widths {
			for j := drawn; j < drawn+fit && j < len(wrapped[i]); j++ {
				drawRunLine(pdf, wrapped[i][j], x+0.5, top+1.2+float64(j-drawn)*tableLine, tableLine, tableSize)
			}
			x += widths[i]
		}
		pdf.SetY(top + float64(fit)*tableLine + 2.4)
		drawn += fit
	}
	bottom := pdf.GetY()
	setColour(pdf.SetDrawColor, ruleColour)
	pdf.SetLineWidth(0.2)
	if header {
		setColour(pdf.SetDrawColor, inkColour)
		pdf.SetLineWidth(0.4)
	}
	pdf.Line(marginLeft, bottom, marginLeft+contentWidth, bottom)
}

func writeImage(pdf *fpdf.Fpdf, block Block, assetsDir string) error {
	if assetsDir == "" {
		return fmt.Errorf("document has an image (%s) but no assets directory was given", block.Source)
	}
	// rendering runs outside the sandbox, so a symlink planted in the assets
	// directory would otherwise read any file the host can reach
	path, err := resolveInside(assetsDir, block.Source)
	if err != nil {
		return err
	}

	info := pdf.RegisterImageOptions(path, fpdf.ImageOptions{ReadDpi: true})
	if info == nil {
		return fmt.Errorf("image %q could not be read as a jpeg, png or gif", block.Source)
	}

	width, height := info.Extent()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("image %q reports a zero size", block.Source)
	}
	scale := contentWidth / width
	if height*scale > imageMaxHeight {
		scale = imageMaxHeight / height
	}
	drawWidth, drawHeight := width*scale, height*scale

	captionHeight := 0.0
	if block.Alt != "" {
		captionHeight = 5
	}

	_, pageHeight := pdf.GetPageSize()
	if pdf.GetY()+drawHeight+captionHeight+4 > pageHeight-marginBottom {
		pdf.AddPage()
	}

	pdf.Ln(2)
	x := marginLeft + (contentWidth-drawWidth)/2
	pdf.ImageOptions(path, x, pdf.GetY(), drawWidth, drawHeight, true, fpdf.ImageOptions{ReadDpi: true}, 0, "")

	if block.Alt != "" {
		pdf.Ln(1.5)
		pdf.SetFont("Helvetica", "", captionSize)
		setColour(pdf.SetTextColor, mutedColour)
		pdf.CellFormat(contentWidth, 4, block.Alt, "", 1, "C", false, 0, "")
		setColour(pdf.SetTextColor, inkColour)
	}
	pdf.Ln(3)
	return nil
}

func writeCode(pdf *fpdf.Fpdf, block Block) {
	pdf.SetFont("Courier", "", codeSize)
	const pad = 2.0
	var lines []string
	for _, line := range strings.Split(block.Text, "\n") {
		// a long line is broken by width rather than run off the page edge
		split := pdf.SplitLines([]byte(line), contentWidth-2*pad)
		if len(split) == 0 {
			lines = append(lines, "")
			continue
		}
		for _, piece := range split {
			lines = append(lines, string(piece))
		}
	}

	_, pageHeight := pdf.GetPageSize()
	drawn := 0
	for drawn < len(lines) {
		available := pageHeight - marginBottom - pdf.GetY() - 3
		fit := int(available / tableLine)
		if fit < 1 {
			if pdf.Error() != nil {
				// AddPage is a no-op once fpdf has failed, so this would spin
				return
			}
			pdf.AddPage()
			continue
		}
		if fit > len(lines)-drawn {
			fit = len(lines) - drawn
		}

		height := float64(fit)*tableLine + 3
		top := pdf.GetY()
		setColour(pdf.SetFillColor, codeFillShade)
		pdf.Rect(marginLeft, top, contentWidth, height, "F")
		for i, line := range lines[drawn : drawn+fit] {
			pdf.SetXY(marginLeft+pad, top+1.5+float64(i)*tableLine)
			pdf.CellFormat(contentWidth-2*pad, tableLine, line, "", 0, "L", false, 0, "")
		}
		pdf.SetY(top + height)
		drawn += fit
	}
	pdf.Ln(3)
}

func writeRule(pdf *fpdf.Fpdf) {
	pdf.Ln(3)
	setColour(pdf.SetDrawColor, ruleColour)
	pdf.SetLineWidth(0.2)
	y := pdf.GetY()
	pdf.Line(marginLeft, y, marginLeft+contentWidth, y)
	pdf.Ln(4)
}

// writeRuns flows inline runs at the current position, switching font per run
// so bold, italic, code and links keep their marks across a wrap. bold makes
// every run bold, which is how a heading keeps its weight.
func writeRuns(pdf *fpdf.Fpdf, runs []Run, lineHeight, size float64, bold bool) {
	for _, run := range runs {
		if bold {
			run.Bold = true
		}
		applyRunFont(pdf, run, size)
		if run.Link != "" {
			pdf.WriteLinkString(lineHeight, run.Text, run.Link)
			continue
		}
		pdf.Write(lineHeight, run.Text)
	}
}

// applyRunFont selects the face a run is drawn in and returns nothing, so
// measuring and drawing can never disagree about the font.
func applyRunFont(pdf *fpdf.Fpdf, run Run, baseSize float64) {
	style := ""
	if run.Bold {
		style += "B"
	}
	if run.Italic {
		style += "I"
	}
	if run.Link != "" {
		style += "U"
	}
	if run.Code {
		// Courier has no underline face, so a code link keeps only its weight
		pdf.SetFont("Courier", strings.ReplaceAll(style, "U", ""), baseSize-0.5)
		return
	}
	pdf.SetFont("Helvetica", style, baseSize)
}

// wrapRuns breaks runs into lines that fit width, measuring each word in the
// font it will be drawn in rather than breaking it mid-word.
func wrapRuns(pdf *fpdf.Fpdf, runs []Run, width, baseSize float64) [][]Run {
	var lines [][]Run
	var current []Run
	used := 0.0

	appendToCurrent := func(run Run, text string) {
		if len(current) > 0 {
			last := &current[len(current)-1]
			if last.Bold == run.Bold && last.Italic == run.Italic && last.Code == run.Code && last.Link == run.Link {
				last.Text += text
				return
			}
		}
		piece := run
		piece.Text = text
		current = append(current, piece)
	}

	for _, run := range runs {
		applyRunFont(pdf, run, baseSize)
		for _, word := range splitKeepingSpaces(run.Text) {
			wordWidth := pdf.GetStringWidth(word)
			if used+wordWidth > width && used > 0 {
				lines = append(lines, current)
				current, used = nil, 0
				if strings.TrimSpace(word) == "" {
					// a space that fell at a line break is dropped, not carried over
					continue
				}
			}
			appendToCurrent(run, word)
			used += wordWidth
		}
	}
	if len(current) > 0 {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		return [][]Run{{}}
	}
	return lines
}

// splitKeepingSpaces breaks text into words and the spaces between them, so
// wrapping can drop a space at a line break without losing one mid-line.
func splitKeepingSpaces(text string) []string {
	var pieces []string
	start := 0
	for i, r := range text {
		if r != ' ' {
			continue
		}
		if i > start {
			pieces = append(pieces, text[start:i])
		}
		pieces = append(pieces, " ")
		start = i + 1
	}
	if start < len(text) {
		pieces = append(pieces, text[start:])
	}
	return pieces
}

// drawRunLine writes one wrapped line at x, advancing the cursor per run.
func drawRunLine(pdf *fpdf.Fpdf, line []Run, x, y, lineHeight, baseSize float64) {
	pdf.SetXY(x, y)
	for _, run := range line {
		applyRunFont(pdf, run, baseSize)
		width := pdf.GetStringWidth(run.Text)
		// a cell, never Write: flowing would wrap at the page margin rather than
		// the box, and could fire a page break the caller has already measured around
		x, y := pdf.GetX(), pdf.GetY()
		pdf.CellFormat(width, lineHeight, run.Text, "", 0, "L", false, 0, "")
		if run.Link != "" {
			pdf.LinkString(x, y, width, lineHeight, run.Link)
		}
	}
}

func setColour(apply func(int, int, int), colour [3]int) {
	apply(colour[0], colour[1], colour[2])
}

// encodeBlocks converts every string in the document to Windows-1252 so the
// PDF's core fonts render it.
func encodeBlocks(blocks []Block) ([]Block, error) {
	encoder := charmap.Windows1252.NewEncoder()
	failedKind := ""
	convert := func(text string) string {
		encoded, err := encoder.String(text)
		if err != nil {
			// the text is not named: by this point it may hold a credential
			failedKind = "document text"
			return text
		}
		return encoded
	}

	out := mapBlockText(blocks, convert)
	if failedKind != "" {
		return nil, fmt.Errorf("%s contains a character that cannot be rendered into a PDF", failedKind)
	}
	return out, nil
}
