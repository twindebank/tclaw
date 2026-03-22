package restauranttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"tclaw/mcp"
)

// commonArgs is embedded in all restaurant tool args to handle optional
// inline credential persistence (same pattern as TfL's api_key passthrough).
type commonArgs struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key"`
	AuthToken string `json:"auth_token"`
}

// resolveProvider looks up the provider by name, defaulting to "resy" if empty.
func resolveProvider(providers map[string]Provider, name string) (Provider, error) {
	if name == "" {
		name = "resy"
	}
	p, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown restaurant provider %q — supported: resy", name)
	}
	return p, nil
}

// persistInlineCredentials stores credentials if passed inline on any tool call.
func persistInlineCredentials(ctx context.Context, provider Provider, apiKey, authToken string) {
	if apiKey == "" && authToken == "" {
		return
	}
	creds := map[string]string{}
	if apiKey != "" {
		creds["api_key"] = apiKey
	}
	if authToken != "" {
		creds["auth_token"] = authToken
	}
	if err := provider.PersistCredentials(ctx, creds); err != nil {
		slog.Warn("failed to persist inline restaurant credentials", "provider", provider.Name(), "err", err)
	}
}

func makeHandler(name string, providers map[string]Provider, deps Deps) mcp.ToolHandler {
	switch name {
	case "restaurant_set_credentials":
		return setCredentialsHandler(providers)
	case "restaurant_search":
		return searchHandler(providers)
	case "restaurant_availability":
		return availabilityHandler(providers)
	case "restaurant_book":
		return bookHandler(providers)
	case "restaurant_cancel":
		return cancelHandler(providers)
	case "restaurant_list_bookings":
		return listBookingsHandler(providers)
	default:
		return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("unknown restaurant tool: %s", name)
		}
	}
}

func setCredentialsHandler(providers map[string]Provider) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Provider  string `json:"provider"`
			APIKey    string `json:"api_key"`
			AuthToken string `json:"auth_token"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		p, err := resolveProvider(providers, a.Provider)
		if err != nil {
			return nil, err
		}

		creds := map[string]string{
			"api_key":    a.APIKey,
			"auth_token": a.AuthToken,
		}
		if err := p.PersistCredentials(ctx, creds); err != nil {
			return nil, fmt.Errorf("store credentials: %w", err)
		}

		return json.Marshal(map[string]string{
			"status":   "ok",
			"provider": p.Name(),
			"message":  fmt.Sprintf("credentials stored for %s — restaurant tools are now ready to use", p.Name()),
		})
	}
}

func searchHandler(providers map[string]Provider) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			Query     string  `json:"query"`
			Lat       float64 `json:"lat"`
			Long      float64 `json:"long"`
			Day       string  `json:"day"`
			PartySize int     `json:"party_size"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		if a.Day == "" {
			return nil, fmt.Errorf("day is required (YYYY-MM-DD format)")
		}

		p, err := resolveProvider(providers, a.Provider)
		if err != nil {
			return nil, err
		}
		persistInlineCredentials(ctx, p, a.APIKey, a.AuthToken)

		return p.Search(ctx, SearchParams{
			Query:     a.Query,
			Lat:       a.Lat,
			Long:      a.Long,
			Day:       a.Day,
			PartySize: a.PartySize,
		})
	}
}

func availabilityHandler(providers map[string]Provider) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			VenueID   string `json:"venue_id"`
			Day       string `json:"day"`
			PartySize int    `json:"party_size"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		if a.VenueID == "" {
			return nil, fmt.Errorf("venue_id is required")
		}
		if a.Day == "" {
			return nil, fmt.Errorf("day is required (YYYY-MM-DD format)")
		}

		p, err := resolveProvider(providers, a.Provider)
		if err != nil {
			return nil, err
		}
		persistInlineCredentials(ctx, p, a.APIKey, a.AuthToken)

		return p.Availability(ctx, AvailabilityParams{
			VenueID:   a.VenueID,
			Day:       a.Day,
			PartySize: a.PartySize,
		})
	}
}

func bookHandler(providers map[string]Provider) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			ConfigID        string `json:"config_id"`
			Day             string `json:"day"`
			PartySize       int    `json:"party_size"`
			PaymentMethodID int    `json:"payment_method_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		if a.ConfigID == "" {
			return nil, fmt.Errorf("config_id is required")
		}
		if a.Day == "" {
			return nil, fmt.Errorf("day is required (YYYY-MM-DD format)")
		}
		if a.PartySize == 0 {
			return nil, fmt.Errorf("party_size is required")
		}
		if a.PaymentMethodID == 0 {
			return nil, fmt.Errorf("payment_method_id is required")
		}

		p, err := resolveProvider(providers, a.Provider)
		if err != nil {
			return nil, err
		}
		persistInlineCredentials(ctx, p, a.APIKey, a.AuthToken)

		result, err := p.Book(ctx, BookParams{
			ConfigID:        a.ConfigID,
			Day:             a.Day,
			PartySize:       a.PartySize,
			PaymentMethodID: a.PaymentMethodID,
		})
		if errors.Is(err, ErrNotSupported) {
			return nil, fmt.Errorf("booking is not supported on %s", p.Name())
		}
		return result, err
	}
}

func cancelHandler(providers map[string]Provider) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
			ReservationID string `json:"reservation_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		if a.ReservationID == "" {
			return nil, fmt.Errorf("reservation_id is required")
		}

		p, err := resolveProvider(providers, a.Provider)
		if err != nil {
			return nil, err
		}
		persistInlineCredentials(ctx, p, a.APIKey, a.AuthToken)

		result, err := p.CancelBooking(ctx, CancelParams{
			ReservationID: a.ReservationID,
		})
		if errors.Is(err, ErrNotSupported) {
			return nil, fmt.Errorf("cancellation is not supported on %s — cancel directly on the platform", p.Name())
		}
		return result, err
	}
}

func listBookingsHandler(providers map[string]Provider) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			commonArgs
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		p, err := resolveProvider(providers, a.Provider)
		if err != nil {
			return nil, err
		}
		persistInlineCredentials(ctx, p, a.APIKey, a.AuthToken)

		result, err := p.ListBookings(ctx)
		if errors.Is(err, ErrNotSupported) {
			return nil, fmt.Errorf("listing bookings is not supported on %s — check your reservations directly on the platform", p.Name())
		}
		return result, err
	}
}
