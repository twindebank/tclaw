package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"

	"tclaw/internal/mcp"
)

// commonArgs is embedded in all TfL tool args to handle the optional api_key.
type commonArgs struct {
	APIKey string `json:"api_key"`
}

// persistAPIKey stores the API key if provided, making it available for future calls.
// Returns an error if persistence fails — the caller decides whether to surface it.
func persistAPIKey(ctx context.Context, deps Deps, key string) error {
	if key == "" {
		return nil
	}
	if err := deps.SecretStore.Set(ctx, APIKeyStoreKey, key); err != nil {
		return fmt.Errorf("persist TfL API key: %w", err)
	}
	return nil
}

// makeHandler returns a ToolHandler that dispatches to the correct handler
// based on tool name. Every handler persists the api_key if provided.
func makeHandler(name string, deps Deps) mcp.ToolHandler {
	switch name {
	case ToolLineStatus:
		return lineStatusHandler(deps)
	case ToolJourney:
		return journeyHandler(deps)
	case ToolArrivals:
		return arrivalsHandler(deps)
	case ToolStopSearch:
		return stopSearchHandler(deps)
	case ToolDisruptions:
		return disruptionsHandler(deps)
	case ToolRoadStatus:
		return roadStatusHandler(deps)
	default:
		return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("unknown tfl tool: %s", name)
		}
	}
}

func lineStatusHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			Modes string `json:"modes"`
			Lines string `json:"lines"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		// Best-effort — log but don't fail the tool call if persistence fails.
		if err := persistAPIKey(ctx, deps, a.APIKey); err != nil {
			slog.Warn("failed to persist TfL API key", "err", err)
		}

		// Specific lines take priority over modes.
		if a.Lines != "" {
			return apiGet(ctx, deps, "/Line/"+url.PathEscape(a.Lines)+"/Status", nil)
		}

		modes := a.Modes
		if modes == "" {
			modes = "tube"
		}
		return apiGet(ctx, deps, "/Line/Mode/"+url.PathEscape(modes)+"/Status", nil)
	}
}

func journeyHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			From   string `json:"from"`
			To     string `json:"to"`
			Date   string `json:"date"`
			Time   string `json:"time"`
			TimeIs string `json:"time_is"`
			Mode   string `json:"mode"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		// Best-effort — log but don't fail the tool call if persistence fails.
		if err := persistAPIKey(ctx, deps, a.APIKey); err != nil {
			slog.Warn("failed to persist TfL API key", "err", err)
		}

		if a.From == "" || a.To == "" {
			return nil, fmt.Errorf("from and to are required")
		}

		query := url.Values{}
		if a.Date != "" {
			query.Set("date", a.Date)
		}
		if a.Time != "" {
			query.Set("time", a.Time)
		}
		if a.TimeIs != "" {
			query.Set("timeIs", a.TimeIs)
		}
		if a.Mode != "" {
			query.Set("mode", a.Mode)
		}

		path := "/Journey/JourneyResults/" + url.PathEscape(a.From) + "/to/" + url.PathEscape(a.To)
		raw, err := apiGet(ctx, deps, path, query)
		if err != nil {
			return nil, err
		}
		return summariseJourneyResponse(raw)
	}
}

func arrivalsHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			StopID string `json:"stop_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		// Best-effort — log but don't fail the tool call if persistence fails.
		if err := persistAPIKey(ctx, deps, a.APIKey); err != nil {
			slog.Warn("failed to persist TfL API key", "err", err)
		}

		if a.StopID == "" {
			return nil, fmt.Errorf("stop_id is required")
		}

		return apiGet(ctx, deps, "/StopPoint/"+url.PathEscape(a.StopID)+"/Arrivals", nil)
	}
}

func stopSearchHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			Query string `json:"query"`
			Modes string `json:"modes"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		// Best-effort — log but don't fail the tool call if persistence fails.
		if err := persistAPIKey(ctx, deps, a.APIKey); err != nil {
			slog.Warn("failed to persist TfL API key", "err", err)
		}

		if a.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		query := url.Values{}
		if a.Modes != "" {
			query.Set("modes", a.Modes)
		}

		return apiGet(ctx, deps, "/StopPoint/Search/"+url.PathEscape(a.Query), query)
	}
}

func disruptionsHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			Modes string `json:"modes"`
			Lines string `json:"lines"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		// Best-effort — log but don't fail the tool call if persistence fails.
		if err := persistAPIKey(ctx, deps, a.APIKey); err != nil {
			slog.Warn("failed to persist TfL API key", "err", err)
		}

		if a.Lines != "" {
			return apiGet(ctx, deps, "/Line/"+url.PathEscape(a.Lines)+"/Disruption", nil)
		}

		modes := a.Modes
		if modes == "" {
			modes = "tube"
		}
		return apiGet(ctx, deps, "/Line/Mode/"+url.PathEscape(modes)+"/Disruption", nil)
	}
}

func roadStatusHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			Roads string `json:"roads"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		// Best-effort — log but don't fail the tool call if persistence fails.
		if err := persistAPIKey(ctx, deps, a.APIKey); err != nil {
			slog.Warn("failed to persist TfL API key", "err", err)
		}

		if a.Roads != "" {
			return apiGet(ctx, deps, "/Road/"+url.PathEscape(a.Roads)+"/Status", nil)
		}

		return apiGet(ctx, deps, "/Road", nil)
	}
}

// --- journey response summarisation ---

// tflJourneyResponse is a minimal subset of the TfL journey planner API response,
// containing only the fields needed for a useful summary.
type tflJourneyResponse struct {
	Journeys []tflJourney `json:"journeys"`
}

type tflJourney struct {
	Duration        int      `json:"duration"`
	ArrivalDateTime string   `json:"arrivalDateTime"`
	Legs            []tflLeg `json:"legs"`
}

type tflLeg struct {
	Duration    int            `json:"duration"`
	Mode        tflLegMode     `json:"mode"`
	Instruction tflInstruction `json:"instruction"`
}

type tflLegMode struct {
	Name string `json:"name"`
}

type tflInstruction struct {
	Summary string `json:"summary"`
}

// journeySummary is the compact output returned to the agent instead of the raw API response.
type journeySummary struct {
	Journeys []journeyOption `json:"journeys"`
}

type journeyOption struct {
	DurationMinutes int          `json:"duration_minutes"`
	ArrivalTime     string       `json:"arrival_time"`
	Legs            []legSummary `json:"legs"`
}

type legSummary struct {
	Mode            string `json:"mode"`
	Summary         string `json:"summary"`
	DurationMinutes int    `json:"duration_minutes"`
}

// summariseJourneyResponse parses the raw TfL journey API response and returns
// a compact summary of up to 3 journey options, each with total duration,
// arrival time, and a per-leg breakdown (mode, instruction, duration).
//
// The raw response can exceed 100k characters — this trims it to only what the
// agent needs to answer a journey planning query.
func summariseJourneyResponse(raw json.RawMessage) (json.RawMessage, error) {
	var resp tflJourneyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse journey response: %w", err)
	}

	journeys := resp.Journeys
	if len(journeys) > 3 {
		journeys = journeys[:3]
	}

	summary := journeySummary{
		Journeys: make([]journeyOption, 0, len(journeys)),
	}
	for _, j := range journeys {
		// Extract HH:MM from "2026-04-12T15:30:00" format — take chars 11–15.
		arrivalTime := j.ArrivalDateTime
		if len(arrivalTime) >= 16 {
			arrivalTime = arrivalTime[11:16]
		}

		legs := make([]legSummary, 0, len(j.Legs))
		for _, l := range j.Legs {
			legs = append(legs, legSummary{
				Mode:            l.Mode.Name,
				Summary:         l.Instruction.Summary,
				DurationMinutes: l.Duration,
			})
		}

		summary.Journeys = append(summary.Journeys, journeyOption{
			DurationMinutes: j.Duration,
			ArrivalTime:     arrivalTime,
			Legs:            legs,
		})
	}

	result, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("marshal journey summary: %w", err)
	}
	return json.RawMessage(result), nil
}
