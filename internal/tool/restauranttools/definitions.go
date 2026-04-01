package restauranttools

import (
	"encoding/json"

	"tclaw/internal/mcp"
)

const (
	ToolSetCredentials = "restaurant_set_credentials"
	ToolSearch         = "restaurant_search"
	ToolAvailability   = "restaurant_availability"
	ToolBook           = "restaurant_book"
	ToolCancel         = "restaurant_cancel"
	ToolListBookings   = "restaurant_list_bookings"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolSetCredentials, ToolSearch, ToolAvailability,
		ToolBook, ToolCancel, ToolListBookings,
	}
}

// infoToolDefs are always registered — they let the agent learn about the
// provider and set up credentials.
var infoToolDefs = []mcp.ToolDef{
	{
		Name: ToolSetCredentials,
		Description: "Set up credentials for a restaurant booking provider. Call with no parameters " +
			"to trigger the secure credential collection flow (returns CREDENTIALS_NEEDED if not yet stored). " +
			"Call with api_key and auth_token to store them directly.\n\n" +
			"For Resy: get your api_key and auth_token from browser dev tools on resy.com — " +
			"open Network tab, find any API request, copy the Authorization header value " +
			"(after 'ResyAPI api_key=') and the x-resy-auth-token header value.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Restaurant platform. Currently supported: resy.",
					"default": "resy"
				},
				"api_key": {
					"type": "string",
					"description": "API key for the provider."
				},
				"auth_token": {
					"type": "string",
					"description": "Auth token for the provider."
				}
			}
		}`),
	},
}

// operationalToolDefs are only registered when credentials are configured.
var operationalToolDefs = []mcp.ToolDef{
	{
		Name: ToolSearch,
		Description: "Search for restaurants. Returns venue IDs, names, and availability overview. " +
			"Use restaurant_availability with a venue_id from the results to see specific time slots.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Restaurant platform. Currently supported: resy.",
					"default": "resy"
				},
				"query": {
					"type": "string",
					"description": "Restaurant name or search term."
				},
				"lat": {
					"type": "number",
					"description": "Latitude for location-based search. Default: 0 (no location filter)."
				},
				"long": {
					"type": "number",
					"description": "Longitude for location-based search. Default: 0 (no location filter)."
				},
				"day": {
					"type": "string",
					"description": "Date in YYYY-MM-DD format (e.g. '2026-04-15')."
				},
				"party_size": {
					"type": "integer",
					"description": "Number of guests. Default: 2."
				}
			},
			"required": ["day"]
		}`),
	},
	{
		Name: ToolAvailability,
		Description: "Check available reservation time slots for a specific restaurant. " +
			"Returns config_ids needed for booking, along with slot times and types (dining room, bar, etc.).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Restaurant platform. Currently supported: resy.",
					"default": "resy"
				},
				"venue_id": {
					"type": "string",
					"description": "Restaurant/venue ID from restaurant_search results."
				},
				"day": {
					"type": "string",
					"description": "Date in YYYY-MM-DD format."
				},
				"party_size": {
					"type": "integer",
					"description": "Number of guests. Default: 2."
				}
			},
			"required": ["venue_id", "day"]
		}`),
	},
	{
		Name: ToolBook,
		Description: "Make a restaurant reservation. This creates a REAL booking — always confirm " +
			"details with the user before calling. Requires a config_id from restaurant_availability. " +
			"The booking uses a payment method already saved in the user's account on the provider platform.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Restaurant platform. Currently supported: resy.",
					"default": "resy"
				},
				"config_id": {
					"type": "string",
					"description": "Config ID from restaurant_availability results."
				},
				"day": {
					"type": "string",
					"description": "Date in YYYY-MM-DD format."
				},
				"party_size": {
					"type": "integer",
					"description": "Number of guests."
				},
				"payment_method_id": {
					"type": "integer",
					"description": "Payment method ID from the user's account on the provider platform."
				}
			},
			"required": ["config_id", "day", "party_size", "payment_method_id"]
		}`),
	},
	{
		Name:        ToolCancel,
		Description: "Cancel an existing restaurant reservation.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Restaurant platform. Currently supported: resy.",
					"default": "resy"
				},
				"reservation_id": {
					"type": "string",
					"description": "Reservation ID to cancel."
				}
			},
			"required": ["reservation_id"]
		}`),
	},
	{
		Name:        ToolListBookings,
		Description: "List upcoming restaurant reservations for the authenticated user.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Restaurant platform. Currently supported: resy.",
					"default": "resy"
				}
			}
		}`),
	},
}
