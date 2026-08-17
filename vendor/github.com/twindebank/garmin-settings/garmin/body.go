package garmin

import (
	"log/slog"
	"net/http"
)

// closeBody closes a response body, logging a failure instead of discarding it.
//
// The caller cannot act on a close error — the response has already been read by the time this
// runs — but a silent `_ = resp.Body.Close()` hides the one case that matters: a transport that
// consistently fails to close is leaking connections, and the only symptom would be exhaustion much
// later. Logging keeps it visible without inventing an error path that callers cannot use.
func closeBody(response *http.Response) {
	if err := response.Body.Close(); err != nil {
		slog.Warn("garmin: failed to close response body",
			"method", response.Request.Method,
			"url", response.Request.URL.Redacted(),
			"err", err)
	}
}
