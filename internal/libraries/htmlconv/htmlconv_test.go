package htmlconv_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/htmlconv"
)

func TestToText(t *testing.T) {
	t.Run("plain text passthrough", func(t *testing.T) {
		got := htmlconv.ToText("hello world")
		require.Equal(t, "hello world", got)
	})

	t.Run("strips script and style", func(t *testing.T) {
		input := `<html><head><style>body{color:red}</style></head><body>
			<script>alert('x')</script>
			<p>visible text</p>
		</body></html>`
		got := htmlconv.ToText(input)
		require.NotContains(t, got, "color:red")
		require.NotContains(t, got, "alert")
		require.Contains(t, got, "visible text")
	})

	t.Run("preserves link text with URL", func(t *testing.T) {
		input := `<p>Click <a href="https://example.com">here</a> for info</p>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "here (https://example.com)")
		require.Contains(t, got, "Click")
	})

	t.Run("link with matching text omits redundant URL", func(t *testing.T) {
		input := `<a href="https://example.com">https://example.com</a>`
		got := htmlconv.ToText(input)
		require.Equal(t, "https://example.com", got)
	})

	t.Run("bare link with no text outputs href", func(t *testing.T) {
		input := `<a href="https://example.com"></a>`
		got := htmlconv.ToText(input)
		require.Equal(t, "https://example.com", got)
	})

	t.Run("strips javascript hrefs", func(t *testing.T) {
		input := `<a href="javascript:void(0)">click me</a>`
		got := htmlconv.ToText(input)
		require.Equal(t, "click me", got)
		require.NotContains(t, got, "javascript")
	})

	t.Run("block elements insert line breaks", func(t *testing.T) {
		input := `<div>first</div><div>second</div>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "first\nsecond")
	})

	t.Run("paragraphs insert line breaks", func(t *testing.T) {
		input := `<p>paragraph one</p><p>paragraph two</p>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "paragraph one\nparagraph two")
	})

	t.Run("br inserts newline", func(t *testing.T) {
		input := `line one<br>line two`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "line one\nline two")
	})

	t.Run("simple table", func(t *testing.T) {
		input := `<table>
			<tr><th>Item</th><th>Price</th></tr>
			<tr><td>Hotel</td><td>£150</td></tr>
			<tr><td>Flight</td><td>£300</td></tr>
		</table>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "Item | Price")
		require.Contains(t, got, "Hotel | £150")
		require.Contains(t, got, "Flight | £300")
	})

	t.Run("table skips empty cells", func(t *testing.T) {
		// Layout tables often have empty spacer cells.
		input := `<table>
			<tr><td></td><td>content</td><td>  </td></tr>
		</table>`
		got := htmlconv.ToText(input)
		require.Equal(t, "content", got)
	})

	t.Run("nested layout table extracts content", func(t *testing.T) {
		// Marketing emails use nested tables for layout. The converter should
		// still extract the text content from deep nesting.
		input := `<table><tr><td>
			<table><tr><td>
				<table><tr>
					<td>Your booking</td>
					<td>Total: £499.00</td>
				</tr></table>
			</td></tr></table>
		</td></tr></table>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "Your booking")
		require.Contains(t, got, "Total: £499.00")
	})

	t.Run("unordered list", func(t *testing.T) {
		input := `<ul><li>alpha</li><li>beta</li><li>gamma</li></ul>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "- alpha")
		require.Contains(t, got, "- beta")
		require.Contains(t, got, "- gamma")
	})

	t.Run("headings", func(t *testing.T) {
		input := `<h1>Title</h1><p>body text</p>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "Title")
		require.Contains(t, got, "body text")
	})

	t.Run("html entities decoded", func(t *testing.T) {
		input := `<p>Price: &pound;100 &amp; tax</p>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "£100")
		require.Contains(t, got, "& tax")
	})

	t.Run("collapses excessive blank lines", func(t *testing.T) {
		input := `<p>one</p><p></p><p></p><p></p><p></p><p>two</p>`
		got := htmlconv.ToText(input)
		// Should not have more than 2 consecutive blank lines.
		require.NotContains(t, got, "\n\n\n\n")
	})

	t.Run("booking.com style email", func(t *testing.T) {
		// Simplified version of a Booking.com confirmation email structure.
		input := `<html><body>
			<table width="100%">
				<tr><td>
					<table>
						<tr><td style="font-size:24px"><b>Booking Confirmation</b></td></tr>
					</table>
				</td></tr>
				<tr><td>
					<table>
						<tr>
							<td>Check-in</td>
							<td>15 April 2025</td>
						</tr>
						<tr>
							<td>Check-out</td>
							<td>17 April 2025</td>
						</tr>
						<tr>
							<td>Total price</td>
							<td>¥45,000</td>
						</tr>
					</table>
				</td></tr>
				<tr><td>
					<a href="https://booking.com/confirm/123">View your booking</a>
				</td></tr>
			</table>
		</body></html>`
		got := htmlconv.ToText(input)
		require.Contains(t, got, "Booking Confirmation")
		require.Contains(t, got, "Check-in")
		require.Contains(t, got, "15 April 2025")
		require.Contains(t, got, "Total price")
		require.Contains(t, got, "¥45,000")
		require.Contains(t, got, "View your booking")
		require.Contains(t, got, "https://booking.com/confirm/123")
	})
}
