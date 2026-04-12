package tfl

import (
	"encoding/json"

	"tclaw/internal/mcp"
)

const (
	ToolLineStatus  = "tfl_line_status"
	ToolJourney     = "tfl_journey"
	ToolArrivals    = "tfl_arrivals"
	ToolStopSearch  = "tfl_stop_search"
	ToolDisruptions = "tfl_disruptions"
	ToolRoadStatus  = "tfl_road_status"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolLineStatus, ToolJourney, ToolArrivals,
		ToolStopSearch, ToolDisruptions, ToolRoadStatus,
	}
}

var toolDefs = []mcp.ToolDef{
	{
		Name: ToolLineStatus,
		Description: "Get the status of TfL lines (tube, overground, elizabeth line, DLR, tram). " +
			"Without parameters, returns status for all tube lines. " +
			"Use 'modes' to get status for a specific mode, or 'lines' for specific line names.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"modes": {
					"type": "string",
					"description": "Comma-separated transport modes. Options: tube, overground, elizabeth-line, dlr, tram. Default: tube."
				},
				"lines": {
					"type": "string",
					"description": "Comma-separated specific line names (e.g. 'central,victoria,northern'). Overrides modes if provided."
				},
				"api_key": {
					"type": "string",
					"description": "TfL API key. Only needed on first use — stored encrypted for subsequent calls. Register free at https://api-portal.tfl.gov.uk/products."
				}
			}
		}`),
	},
	{
		Name: ToolJourney,
		Description: "Plan a journey using TfL. Accepts postcodes, station names, coordinates (lat,lon), or NaPTAN IDs " +
			"as from/to locations. Returns up to 3 route options, each with total duration, arrival time, " +
			"and a leg-by-leg breakdown (mode, instruction summary, duration in minutes). " +
			"The full raw TfL API response is also saved to a temp file (path in full_details_path) — " +
			"use Bash with jq to extract specific fields when more detail is needed.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"from": {
					"type": "string",
					"description": "Starting point — postcode (e.g. 'SW1A 1AA'), station name (e.g. 'Kings Cross'), or coordinates (e.g. '51.5074,-0.1278')."
				},
				"to": {
					"type": "string",
					"description": "Destination — same formats as 'from'."
				},
				"date": {
					"type": "string",
					"description": "Travel date in YYYYMMDD format (e.g. '20260315'). Default: today."
				},
				"time": {
					"type": "string",
					"description": "Travel time in HHMM format (e.g. '0830'). Default: now."
				},
				"time_is": {
					"type": "string",
					"description": "Whether the time is 'Departing' or 'Arriving'. Default: Departing.",
					"enum": ["Departing", "Arriving"]
				},
				"mode": {
					"type": "string",
					"description": "Comma-separated transport modes to include (e.g. 'tube,bus,walking'). Default: all modes."
				},
				"api_key": {
					"type": "string",
					"description": "TfL API key. Only needed on first use — stored encrypted for subsequent calls."
				}
			},
			"required": ["from", "to"]
		}`),
	},
	{
		Name: ToolArrivals,
		Description: "Get live arrivals at a TfL stop or station. Returns the next vehicles/trains with estimated arrival times. " +
			"Use tfl_stop_search first to find the stop ID if you only have a name.\n\n" +
			"IMPORTANT: Group stop IDs (e.g. '490G00010561') return empty results — always use individual stop IDs " +
			"(e.g. '490010561W'). To find individual stop IDs from a group ID or location, use tfl_journey: " +
			"the response includes individualStopId in the departure/arrival point details.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"stop_id": {
					"type": "string",
					"description": "NaPTAN individual stop ID (e.g. '490010561W'). Group stop IDs (starting with '490G') return empty — use individual IDs only. Use tfl_stop_search or tfl_journey to find individual IDs."
				},
				"api_key": {
					"type": "string",
					"description": "TfL API key. Only needed on first use — stored encrypted for subsequent calls."
				}
			},
			"required": ["stop_id"]
		}`),
	},
	{
		Name: ToolStopSearch,
		Description: "Search for TfL stops and stations by name. Returns stop IDs (NaPTAN), names, modes served, and locations. " +
			"Use this to find the stop_id needed by tfl_arrivals.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "Search term — station name, bus stop name, or bus stop code (e.g. 'Kings Cross', 'Oxford Circus', '73095')."
				},
				"modes": {
					"type": "string",
					"description": "Filter by comma-separated modes (e.g. 'tube', 'bus', 'tube,bus'). Default: all modes."
				},
				"api_key": {
					"type": "string",
					"description": "TfL API key. Only needed on first use — stored encrypted for subsequent calls."
				}
			},
			"required": ["query"]
		}`),
	},
	{
		Name: ToolDisruptions,
		Description: "Get current disruptions on TfL lines. Returns affected routes, closure details, and severity. " +
			"Without parameters, shows disruptions for all tube lines.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"modes": {
					"type": "string",
					"description": "Comma-separated modes (e.g. 'tube', 'tube,overground'). Default: tube."
				},
				"lines": {
					"type": "string",
					"description": "Comma-separated specific line names (e.g. 'central,northern'). Overrides modes if provided."
				},
				"api_key": {
					"type": "string",
					"description": "TfL API key. Only needed on first use — stored encrypted for subsequent calls."
				}
			}
		}`),
	},
	{
		Name:        ToolRoadStatus,
		Description: "Get traffic status for major London roads. Returns current severity, status description, and any active closures or restrictions.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"roads": {
					"type": "string",
					"description": "Comma-separated road IDs (e.g. 'A2', 'A2,A406,A40'). Omit to get status for all major roads."
				},
				"api_key": {
					"type": "string",
					"description": "TfL API key. Only needed on first use — stored encrypted for subsequent calls."
				}
			}
		}`),
	},
}
