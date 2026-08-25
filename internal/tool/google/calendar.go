package google

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/mcp"
)

type calendarListArgs struct {
	CredentialSet string `json:"credential_set"`
	StartDate     string `json:"start_date"`
	DaysAhead     int    `json:"days_ahead"`
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
	CalendarID    string `json:"calendar_id"`
	PageToken     string `json:"page_token"`
}

// calendarEventsResponse matches the Google Calendar API's events.list response.
type calendarEventsResponse struct {
	Items         []calendarEvent `json:"items"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

type calendarEvent struct {
	ID             string             `json:"id"`
	Summary        string             `json:"summary"`
	Description    string             `json:"description"`
	Location       string             `json:"location"`
	Start          calendarEventTime  `json:"start"`
	End            calendarEventTime  `json:"end"`
	Status         string             `json:"status"`
	HtmlLink       string             `json:"htmlLink"`
	Organizer      calendarAttendee   `json:"organizer"`
	Attendees      []calendarAttendee `json:"attendees"`
	ConferenceData *conferenceData    `json:"conferenceData"`
	RecurringID    string             `json:"recurringEventId"`
	Recurrence     []string           `json:"recurrence"`
}

type calendarEventTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
	TimeZone string `json:"timeZone"`
}

type calendarAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	ResponseStatus string `json:"responseStatus"`
	Self           bool   `json:"self"`
}

type conferenceData struct {
	EntryPoints []conferenceEntryPoint `json:"entryPoints"`
}

type conferenceEntryPoint struct {
	EntryPointType string `json:"entryPointType"`
	URI            string `json:"uri"`
}

// calendarEventSummary is the cleaned-up event returned to the agent.
type calendarEventSummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	AllDay      bool     `json:"all_day"`
	Location    string   `json:"location,omitempty"`
	Description string   `json:"description,omitempty"`
	Organizer   string   `json:"organizer,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	MeetingLink string   `json:"meeting_link,omitempty"`
	Status      string   `json:"status"`
	IsRecurring bool     `json:"is_recurring"`
}

type calendarListToolResponse struct {
	Events        []calendarEventSummary `json:"events"`
	TimeRange     string                 `json:"time_range"`
	EventCount    int                    `json:"event_count"`
	NextPageToken string                 `json:"next_page_token,omitempty"`
}

func calendarListHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a calendarListArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		maxResults := a.MaxResults
		if maxResults <= 0 {
			maxResults = 50
		}
		if maxResults > 250 {
			maxResults = 250
		}

		calendarID := a.CalendarID
		if calendarID == "" {
			calendarID = "primary"
		}

		windowStart, windowEnd, err := calendarTimeWindow(a.StartDate, a.DaysAhead, time.Now())
		if err != nil {
			return nil, err
		}
		timeMin := windowStart.Format(time.RFC3339)
		timeMax := windowEnd.Format(time.RFC3339)

		slog.Info("calendar list starting", "connection", a.CredentialSet, "start_date", a.StartDate, "days_ahead", a.DaysAhead, "query", a.Query)

		params := map[string]any{
			"calendarId":   calendarID,
			"timeMin":      timeMin,
			"timeMax":      timeMax,
			"maxResults":   maxResults,
			"singleEvents": true, // Expand recurring events into individual instances.
			"orderBy":      "startTime",
		}
		if a.Query != "" {
			params["q"] = a.Query
		}
		if a.PageToken != "" {
			params["pageToken"] = a.PageToken
		}

		output, err := runGWS(ctx, deps, gws.Calendar.ListEvents(params))
		if err != nil {
			return nil, fmt.Errorf("list events: %w", annotateCalendarNotFound(err, calendarID, a.CredentialSet))
		}

		var eventsRsp calendarEventsResponse
		if err := json.Unmarshal(output, &eventsRsp); err != nil {
			return nil, fmt.Errorf("parse events response: %w", err)
		}

		summaries := make([]calendarEventSummary, 0, len(eventsRsp.Items))
		for _, event := range eventsRsp.Items {
			if event.Status == "cancelled" {
				continue
			}
			summaries = append(summaries, extractEventSummary(event))
		}

		timeRange := fmt.Sprintf("%s to %s", windowStart.Format("2006-01-02"), windowEnd.Format("2006-01-02"))

		slog.Info("calendar list done", "connection", a.CredentialSet, "event_count", len(summaries))

		return json.Marshal(calendarListToolResponse{
			Events:        summaries,
			TimeRange:     timeRange,
			EventCount:    len(summaries),
			NextPageToken: eventsRsp.NextPageToken,
		})
	}
}

type calendarCreateArgs struct {
	CredentialSet string `json:"credential_set"`
	Title         string `json:"title"`
	Date          string `json:"date"`

	// EndDate is the inclusive last day of a multi-day all-day event (YYYY-MM-DD).
	// When omitted, all-day events default to a single day.
	EndDate string `json:"end_date"`

	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`

	// AllDay marks the event as all-day. Required to create an all-day event —
	// a bare date without times is rejected to avoid accidental all-day events.
	AllDay bool `json:"all_day"`

	// TimeZone is the IANA name for a timed event's local time (e.g. "Asia/Tokyo").
	// Empty defaults to Europe/London. Ignored for all-day events.
	TimeZone string `json:"timezone"`

	Description string `json:"description"`
	Location    string `json:"location"`
	CalendarID  string `json:"calendar_id"`

	// AddMeet adds a Google Meet video conference link to the event.
	AddMeet bool `json:"add_meet"`
}

type calendarCreateToolResponse struct {
	Created         *calendarEventSummary `json:"created,omitempty"`
	DuplicateOf     *calendarEventSummary `json:"duplicate_of,omitempty"`
	DuplicateAction string                `json:"duplicate_action,omitempty"`
}

func calendarCreateHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a calendarCreateArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		if a.Title == "" {
			return nil, fmt.Errorf("title is required")
		}
		if a.Date == "" {
			return nil, fmt.Errorf("date is required (format: YYYY-MM-DD)")
		}

		// Validate date format.
		eventDate, err := time.Parse("2006-01-02", a.Date)
		if err != nil {
			return nil, fmt.Errorf("invalid date format %q — use YYYY-MM-DD", a.Date)
		}

		calendarID := a.CalendarID
		if calendarID == "" {
			calendarID = "primary"
		}

		// Resolve and validate the event's timing up front. This enforces explicit
		// timed-vs-all-day intent so a bare date can't silently become an all-day event.
		timing, err := buildEventTiming(timingInput{
			Date:      a.Date,
			EndDate:   a.EndDate,
			StartTime: a.StartTime,
			EndTime:   a.EndTime,
			AllDay:    a.AllDay,
			TimeZone:  a.TimeZone,
		})
		if err != nil {
			return nil, err
		}

		slog.Info("calendar create starting", "connection", a.CredentialSet, "title", a.Title, "date", a.Date, "all_day", timing.AllDay)

		// Check for duplicates on the same day with similar title.
		duplicate, err := findDuplicate(ctx, deps, calendarID, a.Title, eventDate)
		if err != nil {
			slog.Warn("duplicate check failed, proceeding with create", "error", err)
		}
		if duplicate != nil {
			summary := extractEventSummary(*duplicate)
			slog.Info("calendar create skipped — duplicate found", "existing_id", duplicate.ID, "title", duplicate.Summary)
			return json.Marshal(calendarCreateToolResponse{
				DuplicateOf:     &summary,
				DuplicateAction: "Event already exists on this date with a matching title. Not created. Use google_calendar_update to modify the existing event if needed.",
			})
		}

		// Build the event body.
		eventBody := map[string]any{
			"summary": a.Title,
			"start":   timing.Start,
			"end":     timing.End,
		}

		if a.Description != "" {
			eventBody["description"] = a.Description
		}
		if a.Location != "" {
			eventBody["location"] = a.Location
		}
		if a.AddMeet {
			// conferenceDataVersion=1 must be set as a query param for the API to process conferenceData.
			requestID, err := generateMeetRequestID()
			if err != nil {
				return nil, err
			}
			eventBody["conferenceData"] = map[string]any{
				"createRequest": map[string]any{
					"requestId":             requestID,
					"conferenceSolutionKey": map[string]any{"type": "hangoutsMeet"},
				},
			}
		}

		calendarParams := map[string]any{"calendarId": calendarID}
		if a.AddMeet {
			calendarParams["conferenceDataVersion"] = 1
		}

		output, err := runGWS(ctx, deps, gws.Calendar.InsertEvent(calendarParams, eventBody))
		if err != nil {
			return nil, fmt.Errorf("create event: %w", annotateCalendarNotFound(err, calendarID, a.CredentialSet))
		}

		var created calendarEvent
		if err := json.Unmarshal(output, &created); err != nil {
			return nil, fmt.Errorf("parse created event: %w", err)
		}

		summary := extractEventSummary(created)

		slog.Info("calendar create done", "connection", a.CredentialSet, "event_id", created.ID, "title", a.Title)

		return json.Marshal(calendarCreateToolResponse{
			Created: &summary,
		})
	}
}

type calendarUpdateArgs struct {
	CredentialSet string `json:"credential_set"`
	EventID       string `json:"event_id"`
	CalendarID    string `json:"calendar_id"`

	Title   string `json:"title"`
	Date    string `json:"date"`
	EndDate string `json:"end_date"`

	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`

	// AllDay converts the event to all-day. Only needed when changing the event type.
	AllDay bool `json:"all_day"`

	// TimeZone is the IANA name for a timed event's local time (e.g. "Asia/Tokyo").
	// Empty inherits the event's existing timezone. Ignored for all-day events.
	TimeZone string `json:"timezone"`

	Description string `json:"description"`
	Location    string `json:"location"`
}

type calendarUpdateToolResponse struct {
	Updated *calendarEventSummary `json:"updated"`
}

func calendarUpdateHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a calendarUpdateArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		if a.EventID == "" {
			return nil, fmt.Errorf("event_id is required")
		}

		// Any of these signals a timing change; a bare all_day flag counts because it
		// converts a timed event to all-day.
		timingTouched := a.Date != "" || a.EndDate != "" || a.StartTime != "" || a.EndTime != "" || a.AllDay
		if a.Title == "" && a.Description == "" && a.Location == "" && !timingTouched {
			return nil, fmt.Errorf("nothing to update — provide at least one of title, date, end_date, start_time, end_time, all_day, description, or location")
		}

		calendarID := a.CalendarID
		if calendarID == "" {
			calendarID = "primary"
		}

		// Fetch the existing event first: update is a full PUT, so we merge changes onto
		// the current event to avoid wiping fields we aren't touching (attendees,
		// reminders, conference data, description, etc.).
		getOutput, err := runGWS(ctx, deps, gws.Calendar.GetEvent(map[string]any{
			"calendarId": calendarID,
			"eventId":    a.EventID,
		}))
		if err != nil {
			return nil, fmt.Errorf("get event: %w", annotateCalendarNotFound(err, calendarID, a.CredentialSet))
		}

		var existing calendarEvent
		if err := json.Unmarshal(getOutput, &existing); err != nil {
			return nil, fmt.Errorf("parse existing event: %w", err)
		}
		// Round-trip the full resource as a generic map so unmodified fields are preserved
		// in the PUT body exactly as Google returned them.
		var body map[string]any
		if err := json.Unmarshal(getOutput, &body); err != nil {
			return nil, fmt.Errorf("parse existing event body: %w", err)
		}

		if a.Title != "" {
			body["summary"] = a.Title
		}
		if a.Description != "" {
			body["description"] = a.Description
		}
		if a.Location != "" {
			body["location"] = a.Location
		}

		if timingTouched {
			timing, err := resolveUpdatedTiming(existing, a)
			if err != nil {
				return nil, err
			}
			// Assigning fresh start/end maps fully replaces the old blocks, so switching
			// between timed and all-day drops the previous dateTime/date form cleanly.
			body["start"] = timing.Start
			body["end"] = timing.End
		}

		slog.Info("calendar update starting", "connection", a.CredentialSet, "event_id", a.EventID, "timing_changed", timingTouched)

		updateOutput, err := runGWS(ctx, deps, gws.Calendar.UpdateEvent(
			map[string]any{"calendarId": calendarID, "eventId": a.EventID},
			body,
		))
		if err != nil {
			return nil, fmt.Errorf("update event: %w", annotateCalendarNotFound(err, calendarID, a.CredentialSet))
		}

		var updated calendarEvent
		if err := json.Unmarshal(updateOutput, &updated); err != nil {
			return nil, fmt.Errorf("parse updated event: %w", err)
		}
		summary := extractEventSummary(updated)

		slog.Info("calendar update done", "connection", a.CredentialSet, "event_id", updated.ID)

		return json.Marshal(calendarUpdateToolResponse{Updated: &summary})
	}
}

// calendarTimeWindow returns the start and end times for a calendar list query.
// If startDate is provided (YYYY-MM-DD), it is used as the start; otherwise now's start-of-day is used.
// daysAhead controls the window length from the start (defaults to 7, max 90).
func calendarTimeWindow(startDate string, daysAhead int, now time.Time) (time.Time, time.Time, error) {
	if daysAhead <= 0 {
		daysAhead = 7
	}
	if daysAhead > 90 {
		daysAhead = 90
	}

	var start time.Time
	if startDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", startDate, now.Location())
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date %q — use YYYY-MM-DD", startDate)
		}
		start = parsed
	} else {
		// Start from the beginning of today so we include events already in progress.
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}

	return start, start.AddDate(0, 0, daysAhead), nil
}

// findDuplicate checks if an event with a similar title already exists on the given date.
func findDuplicate(ctx context.Context, deps Deps, calendarID, title string, date time.Time) (*calendarEvent, error) {
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)

	params := map[string]any{
		"calendarId":   calendarID,
		"timeMin":      dayStart.Format(time.RFC3339),
		"timeMax":      dayEnd.Format(time.RFC3339),
		"singleEvents": true,
		"q":            title,
	}

	output, err := runGWS(ctx, deps, gws.Calendar.ListEvents(params))
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}

	var rsp calendarEventsResponse
	if err := json.Unmarshal(output, &rsp); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}

	titleLower := strings.ToLower(strings.TrimSpace(title))
	for i, event := range rsp.Items {
		if event.Status == "cancelled" {
			continue
		}
		if strings.ToLower(strings.TrimSpace(event.Summary)) == titleLower {
			return &rsp.Items[i], nil
		}
	}

	return nil, nil
}

// defaultCalendarTimeZone is used for timed events when no timezone is supplied.
// A travel or trip event should pass an explicit timezone rather than rely on it.
const defaultCalendarTimeZone = "Europe/London"

// timingInput describes the requested date/time for an event before it's resolved
// into Google Calendar start/end blocks.
type timingInput struct {
	Date      string
	EndDate   string
	StartTime string
	EndTime   string
	AllDay    bool

	// TimeZone is an IANA name (e.g. "Asia/Tokyo"). Empty defaults to
	// Europe/London. Only used for timed events.
	TimeZone string
}

// eventTiming holds the resolved start/end blocks for a calendar event body.
type eventTiming struct {
	Start  map[string]string
	End    map[string]string
	AllDay bool
}

// buildEventTiming validates a timingInput and resolves it into Google Calendar
// start/end blocks, enforcing explicit timed-vs-all-day intent so a bare date can
// never silently become an all-day event.
//
// Timed events use a naive local dateTime plus a timeZone field so Google Calendar
// resolves the correct UTC offset (DST-safe, and correct when travelling across
// timezones). We deliberately do NOT embed a numeric offset in the dateTime string:
// the server runs in UTC so a locally-derived offset would always be +00:00 (wrong
// in BST), and a hardcoded offset would break across DST and international zones.
func buildEventTiming(in timingInput) (eventTiming, error) {
	if in.Date == "" {
		return eventTiming{}, fmt.Errorf("date is required (format: YYYY-MM-DD)")
	}
	startDate, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return eventTiming{}, fmt.Errorf("invalid date format %q — use YYYY-MM-DD", in.Date)
	}

	hasTimes := in.StartTime != "" || in.EndTime != ""
	// An end_date only makes sense for an all-day event, so treat it as an all-day signal.
	wantsAllDay := in.AllDay || in.EndDate != ""

	if hasTimes && wantsAllDay {
		return eventTiming{}, fmt.Errorf("conflicting event type: provide start_time+end_time for a timed event, OR all_day=true (with optional end_date) for an all-day event — not both")
	}

	switch {
	case hasTimes:
		if in.StartTime == "" || in.EndTime == "" {
			return eventTiming{}, fmt.Errorf("timed events need BOTH start_time and end_time (format: HH:MM, 24h)")
		}
		if _, err := time.Parse("15:04", in.StartTime); err != nil {
			return eventTiming{}, fmt.Errorf("invalid start_time format %q — use HH:MM (24h)", in.StartTime)
		}
		if _, err := time.Parse("15:04", in.EndTime); err != nil {
			return eventTiming{}, fmt.Errorf("invalid end_time format %q — use HH:MM (24h)", in.EndTime)
		}

		timeZone := in.TimeZone
		if timeZone == "" {
			timeZone = defaultCalendarTimeZone
		}
		if _, err := time.LoadLocation(timeZone); err != nil {
			return eventTiming{}, fmt.Errorf("invalid timezone %q: %w. Use an IANA name, e.g. 'Europe/London', 'Asia/Tokyo', 'America/New_York'", timeZone, err)
		}

		return eventTiming{
			Start: map[string]string{
				"dateTime": fmt.Sprintf("%sT%s:00", in.Date, in.StartTime),
				"timeZone": timeZone,
			},
			End: map[string]string{
				"dateTime": fmt.Sprintf("%sT%s:00", in.Date, in.EndTime),
				"timeZone": timeZone,
			},
		}, nil

	case wantsAllDay:
		// Google Calendar all-day end dates are exclusive.
		var endDate string
		if in.EndDate != "" {
			// Multi-day all-day event: end_date is the inclusive last day, so add 1 day
			// for the exclusive API value.
			endDateParsed, err := time.Parse("2006-01-02", in.EndDate)
			if err != nil {
				return eventTiming{}, fmt.Errorf("invalid end_date format %q — use YYYY-MM-DD", in.EndDate)
			}
			if !endDateParsed.After(startDate) {
				return eventTiming{}, fmt.Errorf("end_date %q must be after date %q", in.EndDate, in.Date)
			}
			endDate = endDateParsed.AddDate(0, 0, 1).Format("2006-01-02")
		} else {
			// Single-day event: end is the next day (exclusive).
			endDate = startDate.AddDate(0, 0, 1).Format("2006-01-02")
		}
		return eventTiming{
			AllDay: true,
			Start:  map[string]string{"date": in.Date},
			End:    map[string]string{"date": endDate},
		}, nil

	default:
		return eventTiming{}, fmt.Errorf("ambiguous event type: provide start_time+end_time for a timed event, or all_day=true for an all-day event — a bare date is not accepted to avoid accidentally creating an all-day event")
	}
}

// resolveUpdatedTiming computes new start/end blocks for an event update, filling in
// the date and timezone from the existing event when the caller omits them. This means
// a time-only edit keeps the event on its original day and in its original timezone —
// important when tweaking trip events booked in another timezone.
func resolveUpdatedTiming(existing calendarEvent, a calendarUpdateArgs) (eventTiming, error) {
	date := a.Date
	if date == "" {
		existingDate, err := existingEventDate(existing)
		if err != nil {
			return eventTiming{}, err
		}
		date = existingDate
	}

	timeZone := a.TimeZone
	if timeZone == "" {
		timeZone = existing.Start.TimeZone
	}

	return buildEventTiming(timingInput{
		Date:      date,
		EndDate:   a.EndDate,
		StartTime: a.StartTime,
		EndTime:   a.EndTime,
		AllDay:    a.AllDay,
		TimeZone:  timeZone,
	})
}

// annotateCalendarNotFound turns a Google 404 into an actionable hint. A 404 while
// loading a specific calendar almost always means the calendarId doesn't exist on THIS
// Google account — the calendar may live on the other credential set (a "shared"
// calendar is often owned on the personal account, not the shared one), or the
// calendar_id is wrong. Non-404 errors are returned unchanged.
func annotateCalendarNotFound(err error, calendarID, credentialSet string) error {
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "notfound") {
		return err
	}
	return fmt.Errorf("calendar %q not found on credential_set %q — it may live on a different Google account (try the other credential_set), or the calendar_id is wrong. Run google_workspace with command \"calendar calendarList list\" on each account to find the correct calendarId and where it lives. underlying: %w", calendarID, credentialSet, err)
}

// existingEventDate returns an event's calendar date (YYYY-MM-DD), whether it's an
// all-day event (Start.Date) or a timed event (date portion of Start.DateTime).
func existingEventDate(event calendarEvent) (string, error) {
	if event.Start.Date != "" {
		return event.Start.Date, nil
	}
	if event.Start.DateTime != "" {
		parsed, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err != nil {
			return "", fmt.Errorf("parse existing event start %q: %w", event.Start.DateTime, err)
		}
		return parsed.Format("2006-01-02"), nil
	}
	return "", fmt.Errorf("existing event has no start date or dateTime")
}

func extractEventSummary(event calendarEvent) calendarEventSummary {
	s := calendarEventSummary{
		ID:     event.ID,
		Title:  event.Summary,
		Status: event.Status,
	}

	if event.Start.Date != "" {
		// All-day event.
		s.AllDay = true
		s.Start = event.Start.Date
		s.End = event.End.Date
	} else {
		s.Start = event.Start.DateTime
		s.End = event.End.DateTime
	}

	if event.Location != "" {
		s.Location = event.Location
	}
	if event.Description != "" {
		// Truncate long descriptions to avoid bloating the response.
		desc := event.Description
		if len(desc) > 500 {
			desc = desc[:500] + "..."
		}
		s.Description = desc
	}

	if event.Organizer.Email != "" {
		if event.Organizer.DisplayName != "" {
			s.Organizer = fmt.Sprintf("%s <%s>", event.Organizer.DisplayName, event.Organizer.Email)
		} else {
			s.Organizer = event.Organizer.Email
		}
	}

	for _, a := range event.Attendees {
		label := a.Email
		if a.DisplayName != "" {
			label = fmt.Sprintf("%s <%s>", a.DisplayName, a.Email)
		}
		if a.ResponseStatus != "" && a.ResponseStatus != "needsAction" {
			label += " (" + a.ResponseStatus + ")"
		}
		s.Attendees = append(s.Attendees, label)
	}

	if event.ConferenceData != nil {
		for _, ep := range event.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" && ep.URI != "" {
				s.MeetingLink = ep.URI
				break
			}
		}
	}

	if event.RecurringID != "" || len(event.Recurrence) > 0 {
		s.IsRecurring = true
	}

	return s
}

// generateMeetRequestID returns a random UUID-format string used as the conferenceData
// requestId. Google Calendar uses this to deduplicate conference creation on retries.
func generateMeetRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate meet request ID: %w", err)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
