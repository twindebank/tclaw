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
			return nil, fmt.Errorf("list events: %w", err)
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
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	Description   string `json:"description"`
	Location      string `json:"location"`
	CalendarID    string `json:"calendar_id"`

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

		isAllDay := a.StartTime == "" && a.EndTime == ""

		slog.Info("calendar create starting", "connection", a.CredentialSet, "title", a.Title, "date", a.Date, "all_day", isAllDay)

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
				DuplicateAction: "Event already exists on this date with a matching title. Not created. Use google_workspace to update the existing event if needed.",
			})
		}

		// Build the event body.
		eventBody := map[string]any{
			"summary": a.Title,
		}

		if isAllDay {
			// All-day event: use date (not dateTime).
			eventBody["start"] = map[string]string{"date": a.Date}
			// Google Calendar all-day end dates are exclusive, so add 1 day.
			endDate := eventDate.AddDate(0, 0, 1).Format("2006-01-02")
			eventBody["end"] = map[string]string{"date": endDate}
		} else {
			// Timed event: build RFC3339 dateTime with local UTC offset.
			_, offset := time.Now().Zone()
			offsetHours := offset / 3600
			offsetMinutes := (offset % 3600) / 60
			offsetStr := fmt.Sprintf("%+03d:%02d", offsetHours, abs(offsetMinutes))

			startTime := a.StartTime
			if startTime == "" {
				return nil, fmt.Errorf("start_time is required for timed events (format: HH:MM)")
			}
			endTime := a.EndTime
			if endTime == "" {
				return nil, fmt.Errorf("end_time is required for timed events (format: HH:MM)")
			}

			// Validate time formats.
			if _, err := time.Parse("15:04", startTime); err != nil {
				return nil, fmt.Errorf("invalid start_time format %q — use HH:MM (24h)", startTime)
			}
			if _, err := time.Parse("15:04", endTime); err != nil {
				return nil, fmt.Errorf("invalid end_time format %q — use HH:MM (24h)", endTime)
			}

			eventBody["start"] = map[string]string{
				"dateTime": fmt.Sprintf("%sT%s:00%s", a.Date, startTime, offsetStr),
			}
			eventBody["end"] = map[string]string{
				"dateTime": fmt.Sprintf("%sT%s:00%s", a.Date, endTime, offsetStr),
			}
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
			return nil, fmt.Errorf("create event: %w", err)
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

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
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
