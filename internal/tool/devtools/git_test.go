package devtools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePRListOutput(t *testing.T) {
	t.Run("empty output means no PR", func(t *testing.T) {
		pr, err := parsePRListOutput("")
		require.NoError(t, err)
		require.Equal(t, prInfo{}, pr)
		require.Equal(t, prStateNone, pr.State)
	})

	t.Run("null output means no PR", func(t *testing.T) {
		pr, err := parsePRListOutput("null\n")
		require.NoError(t, err)
		require.Equal(t, prStateNone, pr.State)
		require.Empty(t, pr.URL)
	})

	t.Run("whitespace-only output means no PR", func(t *testing.T) {
		pr, err := parsePRListOutput("   \n  ")
		require.NoError(t, err)
		require.Equal(t, prStateNone, pr.State)
	})

	t.Run("parses a merged PR — the case that caused the duplicate", func(t *testing.T) {
		pr, err := parsePRListOutput(`{"url":"https://github.com/o/r/pull/154","state":"MERGED"}`)
		require.NoError(t, err)
		require.Equal(t, prStateMerged, pr.State)
		require.Equal(t, "https://github.com/o/r/pull/154", pr.URL)
	})

	t.Run("parses an open PR", func(t *testing.T) {
		pr, err := parsePRListOutput(`{"url":"https://github.com/o/r/pull/12","state":"OPEN"}`)
		require.NoError(t, err)
		require.Equal(t, prStateOpen, pr.State)
	})

	t.Run("parses a closed PR", func(t *testing.T) {
		pr, err := parsePRListOutput(`{"url":"https://github.com/o/r/pull/9","state":"CLOSED"}`)
		require.NoError(t, err)
		require.Equal(t, prStateClosed, pr.State)
	})

	t.Run("returns an error on malformed output", func(t *testing.T) {
		_, err := parsePRListOutput(`not json`)
		require.Error(t, err)
	})
}

func TestShouldCreatePRForEnd(t *testing.T) {
	t.Run("never duplicates an open or merged PR", func(t *testing.T) {
		require.False(t, shouldCreatePRForEnd(prStateOpen))
		require.False(t, shouldCreatePRForEnd(prStateMerged))
	})

	t.Run("creates when there is no PR or it was closed without merging", func(t *testing.T) {
		require.True(t, shouldCreatePRForEnd(prStateNone))
		require.True(t, shouldCreatePRForEnd(prStateClosed))
	})
}

func TestShouldCreatePRForPR(t *testing.T) {
	t.Run("reuses only an open PR", func(t *testing.T) {
		require.False(t, shouldCreatePRForPR(prStateOpen))
	})

	t.Run("opens a new PR for merged, closed, or absent PRs", func(t *testing.T) {
		require.True(t, shouldCreatePRForPR(prStateMerged))
		require.True(t, shouldCreatePRForPR(prStateClosed))
		require.True(t, shouldCreatePRForPR(prStateNone))
	})
}
