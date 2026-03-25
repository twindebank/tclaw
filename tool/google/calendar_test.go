package google

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCalendarTimeWindow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 3, 25, 14, 30, 0, 0, loc) // mid-day Tuesday

	t.Run("defaults to today start-of-day with 7 days ahead", func(t *testing.T) {
		start, end, err := calendarTimeWindow("", 0, now)
		require.NoError(t, err)
		require.Equal(t, "2026-03-25", start.Format("2006-01-02"))
		require.Equal(t, "2026-04-01", end.Format("2006-01-02"))
	})

	t.Run("uses start_date when provided", func(t *testing.T) {
		start, end, err := calendarTimeWindow("2026-06-01", 14, now)
		require.NoError(t, err)
		require.Equal(t, "2026-06-01", start.Format("2006-01-02"))
		require.Equal(t, "2026-06-15", end.Format("2006-01-02"))
	})

	t.Run("start_date in the past is accepted", func(t *testing.T) {
		start, end, err := calendarTimeWindow("2026-01-01", 7, now)
		require.NoError(t, err)
		require.Equal(t, "2026-01-01", start.Format("2006-01-02"))
		require.Equal(t, "2026-01-08", end.Format("2006-01-02"))
	})

	t.Run("clamps days_ahead to 90 max", func(t *testing.T) {
		start, end, err := calendarTimeWindow("", 200, now)
		require.NoError(t, err)
		require.Equal(t, "2026-03-25", start.Format("2006-01-02"))
		require.Equal(t, "2026-06-23", end.Format("2006-01-02")) // 90 days from Mar 25
	})

	t.Run("clamps days_ahead minimum to 7 when zero", func(t *testing.T) {
		_, end, err := calendarTimeWindow("", 0, now)
		require.NoError(t, err)
		require.Equal(t, "2026-04-01", end.Format("2006-01-02"))
	})

	t.Run("clamps days_ahead minimum to 7 when negative", func(t *testing.T) {
		_, end, err := calendarTimeWindow("", -5, now)
		require.NoError(t, err)
		require.Equal(t, "2026-04-01", end.Format("2006-01-02"))
	})

	t.Run("rejects invalid start_date format", func(t *testing.T) {
		_, _, err := calendarTimeWindow("25-03-2026", 7, now)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid start_date")
	})

	t.Run("rejects non-date string", func(t *testing.T) {
		_, _, err := calendarTimeWindow("next-monday", 7, now)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid start_date")
	})
}
