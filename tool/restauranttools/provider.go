package restauranttools

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrNotSupported is returned when a provider doesn't support an operation
// (e.g. OpenTable doesn't support programmatic booking).
var ErrNotSupported = errors.New("operation not supported by this provider")

// ErrCredentialsRequired is returned when the provider's credentials haven't
// been configured yet.
var ErrCredentialsRequired = errors.New("credentials not configured")

// Provider is a restaurant booking platform (Resy, OpenTable, etc.).
// Each method returns raw JSON so the agent sees the full API response
// without tclaw needing to model every provider's response shape.
type Provider interface {
	// Name returns the provider identifier (e.g. "resy", "opentable").
	Name() string

	// Search finds restaurants matching the query.
	Search(ctx context.Context, params SearchParams) (json.RawMessage, error)

	// Availability returns available time slots for a venue.
	Availability(ctx context.Context, params AvailabilityParams) (json.RawMessage, error)

	// Book makes a reservation. Returns ErrNotSupported if the provider
	// doesn't support programmatic booking.
	Book(ctx context.Context, params BookParams) (json.RawMessage, error)

	// CancelBooking cancels an existing reservation. Returns ErrNotSupported
	// if the provider doesn't support programmatic cancellation.
	CancelBooking(ctx context.Context, params CancelParams) (json.RawMessage, error)

	// ListBookings returns the user's upcoming reservations. Returns
	// ErrNotSupported if the provider doesn't support listing bookings.
	ListBookings(ctx context.Context) (json.RawMessage, error)

	// PersistCredentials stores the provider's credentials for future use.
	PersistCredentials(ctx context.Context, credentials map[string]string) error
}

// SearchParams are the inputs for restaurant search.
type SearchParams struct {
	Query     string  `json:"query"`
	Lat       float64 `json:"lat"`
	Long      float64 `json:"long"`
	Day       string  `json:"day"`
	PartySize int     `json:"party_size"`
}

// AvailabilityParams are the inputs for checking time slot availability.
type AvailabilityParams struct {
	VenueID   string `json:"venue_id"`
	Day       string `json:"day"`
	PartySize int    `json:"party_size"`
}

// BookParams are the inputs for making a reservation.
type BookParams struct {
	ConfigID        string `json:"config_id"`
	Day             string `json:"day"`
	PartySize       int    `json:"party_size"`
	PaymentMethodID int    `json:"payment_method_id"`
}

// CancelParams are the inputs for cancelling a reservation.
type CancelParams struct {
	ReservationID string `json:"reservation_id"`
}
