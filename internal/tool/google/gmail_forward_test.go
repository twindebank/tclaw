package google

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildForwardSubject(t *testing.T) {
	t.Run("adds Fwd: prefix", func(t *testing.T) {
		require.Equal(t, "Fwd: Hello", buildForwardSubject("Hello"))
	})

	t.Run("preserves existing fwd: prefix", func(t *testing.T) {
		require.Equal(t, "Fwd: Hello", buildForwardSubject("Fwd: Hello"))
	})

	t.Run("preserves existing FWD: prefix case-insensitively", func(t *testing.T) {
		require.Equal(t, "FWD: Hello", buildForwardSubject("FWD: Hello"))
	})

	t.Run("empty subject", func(t *testing.T) {
		require.Equal(t, "Fwd: ", buildForwardSubject(""))
	})
}

func TestBuildForwardedBlock(t *testing.T) {
	t.Run("all fields present", func(t *testing.T) {
		block := buildForwardedBlock(
			"alice@example.com",
			"bob@example.com",
			"",
			"Mon, 1 Jan 2026",
			"Hello",
			"Original body",
		)
		require.Contains(t, block, "---------- Forwarded message ---------")
		require.Contains(t, block, "From: alice@example.com")
		require.Contains(t, block, "Date: Mon, 1 Jan 2026")
		require.Contains(t, block, "Subject: Hello")
		require.Contains(t, block, "To: bob@example.com")
		require.Contains(t, block, "Original body")
		require.NotContains(t, block, "Cc:")
	})

	t.Run("includes cc when present", func(t *testing.T) {
		block := buildForwardedBlock(
			"alice@example.com",
			"bob@example.com",
			"carol@example.com",
			"Mon, 1 Jan 2026",
			"Hello",
			"Body",
		)
		require.Contains(t, block, "Cc: carol@example.com")
	})

	t.Run("omits missing optional fields", func(t *testing.T) {
		block := buildForwardedBlock("", "", "", "", "Hello", "Body")
		require.Contains(t, block, "---------- Forwarded message ---------")
		require.Contains(t, block, "Subject: Hello")
		require.Contains(t, block, "Body")
		require.NotContains(t, block, "From:")
		require.NotContains(t, block, "Date:")
		require.NotContains(t, block, "To:")
	})

	t.Run("uses CRLF line endings", func(t *testing.T) {
		block := buildForwardedBlock("alice@example.com", "bob@example.com", "", "Mon, 1 Jan 2026", "Hello", "Body")
		require.Contains(t, block, "\r\n")
		require.NotContains(t, block, "\r\nFrom: alice@example.com\nDate:")
	})

	t.Run("body appears after blank line", func(t *testing.T) {
		block := buildForwardedBlock("a@b.com", "c@d.com", "", "Jan 1", "Subj", "The body text")
		require.Contains(t, block, "\r\n\r\nThe body text")
	})
}

func TestBuildForwardReferences(t *testing.T) {
	t.Run("no existing refs", func(t *testing.T) {
		require.Equal(t, "<msg@example.com>", buildForwardReferences("", "<msg@example.com>"))
	})

	t.Run("appends to existing refs", func(t *testing.T) {
		result := buildForwardReferences("<prev@example.com>", "<msg@example.com>")
		require.Equal(t, "<prev@example.com> <msg@example.com>", result)
	})

	t.Run("empty message ID returns existing refs unchanged", func(t *testing.T) {
		require.Equal(t, "<prev@example.com>", buildForwardReferences("<prev@example.com>", ""))
	})

	t.Run("both empty returns empty", func(t *testing.T) {
		require.Equal(t, "", buildForwardReferences("", ""))
	})
}
