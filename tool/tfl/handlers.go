package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"tclaw/mcp"
)

// commonArgs is embedded in all TfL tool args to handle the optional api_key.
type commonArgs struct {
	APIKey string `json:"api_key"`
}

// persistAPIKey stores the API key if provided, making it available for future calls.
func persistAPIKey(ctx context.Context, deps Deps, key string) {
	if key == "" {
		return
	}
	// Best-effort — don't fail the tool call if we can't persist.
	_ = deps.SecretStore.Set(ctx, APIKeyStoreKey, key)
}

// makeHandler returns a ToolHandler that dispatches to the correct handler
// based on tool name. Every handler persists the api_key if provided.
func makeHandler(name string, deps Deps) mcp.ToolHandler {
	switch name {
	case "tfl_line_status":
		return lineStatusHandler(deps)
	case "tfl_journey":
		return journeyHandler(deps)
	case "tfl_arrivals":
		return arrivalsHandler(deps)
	case "tfl_stop_search":
		return stopSearchHandler(deps)
	case "tfl_disruptions":
		return disruptionsHandler(deps)
	case "tfl_road_status":
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
		persistAPIKey(ctx, deps, a.APIKey)

		// Specific lines take priority over modes.
		if a.Lines != "" {
			return apiGet(ctx, deps, "/Line/"+a.Lines+"/Status", nil)
		}

		modes := a.Modes
		if modes == "" {
			modes = "tube"
		}
		return apiGet(ctx, deps, "/Line/Mode/"+modes+"/Status", nil)
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
		persistAPIKey(ctx, deps, a.APIKey)

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
		return apiGet(ctx, deps, path, query)
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
		persistAPIKey(ctx, deps, a.APIKey)

		if a.StopID == "" {
			return nil, fmt.Errorf("stop_id is required")
		}

		return apiGet(ctx, deps, "/StopPoint/"+a.StopID+"/Arrivals", nil)
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
		persistAPIKey(ctx, deps, a.APIKey)

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
		persistAPIKey(ctx, deps, a.APIKey)

		if a.Lines != "" {
			return apiGet(ctx, deps, "/Line/"+a.Lines+"/Disruption", nil)
		}

		modes := a.Modes
		if modes == "" {
			modes = "tube"
		}
		return apiGet(ctx, deps, "/Line/Mode/"+modes+"/Disruption", nil)
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
		persistAPIKey(ctx, deps, a.APIKey)

		if a.Roads != "" {
			return apiGet(ctx, deps, "/Road/"+a.Roads+"/Status", nil)
		}

		return apiGet(ctx, deps, "/Road", nil)
	}
}
