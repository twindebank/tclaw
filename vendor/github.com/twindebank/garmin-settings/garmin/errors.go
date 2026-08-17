package garmin

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrMFARequired is returned by Login when the account has multi-factor auth enabled. The caller
// must collect a code and finish the flow; see PendingLogin.
var ErrMFARequired = errors.New("garmin: multi-factor authentication required")

// ErrNoToken means nothing is stored yet and no credentials were supplied, so there is no way to
// authenticate without a human.
var ErrNoToken = errors.New("garmin: no cached token and no credentials")

// APIError is a non-2xx response from Garmin. The status code carries more meaning than usual with
// this API, so it is kept separate from the message rather than folded into a string:
//
//   - 404 the route does not exist
//   - 405 the route exists but rejects this verb
//   - 406 the route exists but will not produce the requested Content-Type
//   - 500 frequently means a value failed enum validation, not that the server broke
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("garmin: %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, truncate(e.Body, 200))
}

// Retryable reports whether the request is worth sending again unchanged.
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// NotFound distinguishes "no such route" from the other 4xx cases, which mean the route does exist.
func (e *APIError) NotFound() bool { return e.StatusCode == http.StatusNotFound }

// RateLimited reports the 429 that Garmin applies per source IP, most aggressively on login.
func (e *APIError) RateLimited() bool { return e.StatusCode == http.StatusTooManyRequests }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
