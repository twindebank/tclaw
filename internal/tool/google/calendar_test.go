package google

import (
	"fmt"
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

func TestBuildEventTiming(t *testing.T) {
	t.Run("timed event defaults to Europe/London", func(t *testing.T) {
		got, err := buildEventTiming(timingInput{Date: "2026-03-13", StartTime: "17:00", EndTime: "19:00"})
		require.NoError(t, err)
		require.False(t, got.AllDay)
		require.Equal(t, "2026-03-13T17:00:00", got.Start["dateTime"])
		require.Equal(t, "Europe/London", got.Start["timeZone"])
		require.Equal(t, "2026-03-13T19:00:00", got.End["dateTime"])
		require.Equal(t, "Europe/London", got.End["timeZone"])
		require.NotContains(t, got.Start, "date")
	})

	t.Run("timed event honours an explicit timezone", func(t *testing.T) {
		got, err := buildEventTiming(timingInput{Date: "2026-05-20", StartTime: "19:00", EndTime: "21:00", TimeZone: "Asia/Tokyo"})
		require.NoError(t, err)
		require.Equal(t, "Asia/Tokyo", got.Start["timeZone"])
		require.Equal(t, "Asia/Tokyo", got.End["timeZone"])
		require.Equal(t, "2026-05-20T19:00:00", got.Start["dateTime"])
	})

	t.Run("single-day all-day event uses exclusive end date", func(t *testing.T) {
		got, err := buildEventTiming(timingInput{Date: "2026-07-01", AllDay: true})
		require.NoError(t, err)
		require.True(t, got.AllDay)
		require.Equal(t, "2026-07-01", got.Start["date"])
		require.Equal(t, "2026-07-02", got.End["date"])
		require.NotContains(t, got.Start, "dateTime")
	})

	t.Run("multi-day all-day event adds one day to the inclusive end", func(t *testing.T) {
		got, err := buildEventTiming(timingInput{Date: "2026-07-01", EndDate: "2026-07-05", AllDay: true})
		require.NoError(t, err)
		require.True(t, got.AllDay)
		require.Equal(t, "2026-07-01", got.Start["date"])
		require.Equal(t, "2026-07-06", got.End["date"])
	})

	t.Run("end_date alone implies an all-day event", func(t *testing.T) {
		got, err := buildEventTiming(timingInput{Date: "2026-07-01", EndDate: "2026-07-05"})
		require.NoError(t, err)
		require.True(t, got.AllDay)
		require.Equal(t, "2026-07-06", got.End["date"])
	})

	t.Run("rejects a bare date with no times and no all_day", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "ambiguous event type")
	})

	t.Run("rejects only start_time", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01", StartTime: "17:00"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "BOTH")
	})

	t.Run("rejects only end_time", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01", EndTime: "19:00"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "BOTH")
	})

	t.Run("rejects times combined with all_day", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01", StartTime: "17:00", EndTime: "19:00", AllDay: true})
		require.Error(t, err)
		require.Contains(t, err.Error(), "conflicting event type")
	})

	t.Run("rejects times combined with end_date", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01", StartTime: "17:00", EndTime: "19:00", EndDate: "2026-07-02"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "conflicting event type")
	})

	t.Run("rejects a missing date", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{StartTime: "17:00", EndTime: "19:00"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "date is required")
	})

	t.Run("rejects an invalid date format", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "13-03-2026", StartTime: "17:00", EndTime: "19:00"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid date format")
	})

	t.Run("rejects an invalid start_time format", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01", StartTime: "5pm", EndTime: "19:00"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid start_time")
	})

	t.Run("rejects an invalid timezone", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-01", StartTime: "17:00", EndTime: "19:00", TimeZone: "Mars/Olympus"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid timezone")
	})

	t.Run("rejects an end_date not after the start date", func(t *testing.T) {
		_, err := buildEventTiming(timingInput{Date: "2026-07-05", EndDate: "2026-07-05", AllDay: true})
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be after")
	})
}

func TestResolveUpdatedTiming(t *testing.T) {
	timedTokyo := calendarEvent{
		Start: calendarEventTime{DateTime: "2026-05-20T19:00:00+09:00", TimeZone: "Asia/Tokyo"},
		End:   calendarEventTime{DateTime: "2026-05-20T21:00:00+09:00", TimeZone: "Asia/Tokyo"},
	}
	allDay := calendarEvent{
		Start: calendarEventTime{Date: "2026-07-01"},
		End:   calendarEventTime{Date: "2026-07-02"},
	}

	t.Run("time-only change keeps existing date and timezone", func(t *testing.T) {
		got, err := resolveUpdatedTiming(timedTokyo, calendarUpdateArgs{StartTime: "20:00", EndTime: "22:00"})
		require.NoError(t, err)
		require.Equal(t, "2026-05-20T20:00:00", got.Start["dateTime"])
		require.Equal(t, "Asia/Tokyo", got.Start["timeZone"])
		require.Equal(t, "2026-05-20T22:00:00", got.End["dateTime"])
	})

	t.Run("explicit date overrides the existing day", func(t *testing.T) {
		got, err := resolveUpdatedTiming(timedTokyo, calendarUpdateArgs{Date: "2026-05-21", StartTime: "20:00", EndTime: "22:00"})
		require.NoError(t, err)
		require.Equal(t, "2026-05-21T20:00:00", got.Start["dateTime"])
		require.Equal(t, "Asia/Tokyo", got.Start["timeZone"])
	})

	t.Run("explicit timezone overrides the existing zone", func(t *testing.T) {
		got, err := resolveUpdatedTiming(timedTokyo, calendarUpdateArgs{StartTime: "20:00", EndTime: "22:00", TimeZone: "Europe/London"})
		require.NoError(t, err)
		require.Equal(t, "Europe/London", got.Start["timeZone"])
	})

	t.Run("all-day event derives its date from the existing start", func(t *testing.T) {
		got, err := resolveUpdatedTiming(allDay, calendarUpdateArgs{AllDay: true, EndDate: "2026-07-05"})
		require.NoError(t, err)
		require.True(t, got.AllDay)
		require.Equal(t, "2026-07-01", got.Start["date"])
		require.Equal(t, "2026-07-06", got.End["date"])
	})

	t.Run("propagates validation errors from the timing builder", func(t *testing.T) {
		_, err := resolveUpdatedTiming(timedTokyo, calendarUpdateArgs{StartTime: "20:00"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "BOTH")
	})
}

func TestAnnotateCalendarNotFound(t *testing.T) {
	t.Run("adds an actionable hint to a 404/notFound error", func(t *testing.T) {
		raw := fmt.Errorf(`{"error":{"code":404,"message":"Not Found","reason":"notFound"}}`)
		got := annotateCalendarNotFound(raw, "cal@group.calendar.google.com", "google/shared")
		require.Error(t, got)
		require.Contains(t, got.Error(), "not found on credential_set \"google/shared\"")
		require.Contains(t, got.Error(), "different Google account")
		require.Contains(t, got.Error(), "calendarList list")
		require.ErrorIs(t, got, raw)
	})

	t.Run("leaves non-404 errors unchanged", func(t *testing.T) {
		raw := fmt.Errorf("some other failure")
		got := annotateCalendarNotFound(raw, "primary", "google/personal")
		require.Equal(t, raw, got)
	})

	t.Run("returns nil for a nil error", func(t *testing.T) {
		require.NoError(t, annotateCalendarNotFound(nil, "primary", "google/personal"))
	})
}

func TestExistingEventDate(t *testing.T) {
	t.Run("returns the date of an all-day event", func(t *testing.T) {
		got, err := existingEventDate(calendarEvent{Start: calendarEventTime{Date: "2026-07-01"}})
		require.NoError(t, err)
		require.Equal(t, "2026-07-01", got)
	})

	t.Run("returns the date portion of a timed event", func(t *testing.T) {
		got, err := existingEventDate(calendarEvent{Start: calendarEventTime{DateTime: "2026-05-20T19:00:00+09:00"}})
		require.NoError(t, err)
		require.Equal(t, "2026-05-20", got)
	})

	t.Run("errors when the event has no start", func(t *testing.T) {
		_, err := existingEventDate(calendarEvent{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no start")
	})
}
