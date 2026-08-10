package discovery

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ColdStartRetry bounds the retry loop: enough attempts, spread over enough
// wall-clock, to cover a Fly machine waking from autostop without hanging a
// caller whose upstream is truly down.
type ColdStartRetry struct {
	// MaxAttempts is the total number of tries, including the first.
	MaxAttempts int

	// BackoffCap ceilings the doubling delay between attempts.
	BackoffCap time.Duration
}

// DefaultColdStartRetry spreads 9 attempts over ~40s.
//
// The budget is sized from the worst upstream we run rather than a typical one.
// A plain web service answers within a couple of seconds of being woken, but a
// machine that boots a browser before it serves takes far longer: understudy
// measured ~30s from machine start to its port accepting (Fly boots the machine
// in ~1s, then Steel's API needs ~15s and Chromium another ~14s before the front
// door binds), which is ~21s from the wake-triggering request landing. An
// earlier 6-attempt/~8s budget therefore failed every first request after an
// idle period, and — because the claude CLI drops an MCP server whose handshake
// fails at launch — silently cost the agent that server's whole tool surface for
// the turn.
var DefaultColdStartRetry = ColdStartRetry{MaxAttempts: 9, BackoffCap: 10 * time.Second}

// NewColdStartRetryTransport wraps inner to tolerate the connection-level
// failures a sleeping upstream produces while it cold-starts. A Fly machine with
// auto_stop_machines sleeps when idle; the first request wakes it and, until the
// service is listening, the fly-proxy resets the TCP/TLS connection (EOF /
// "connection reset by peer" / "connection refused") instead of returning an
// HTTP status. Those are pre-response transport errors — the upstream never saw
// the request — so replaying the buffered request is safe. Any error that yields
// an HTTP response (4xx/5xx) is returned untouched, and a terminal transport
// error (TLS pin mismatch, caller cancellation) is not retried.
//
// The wrapping client's own Timeout still bounds the total elapsed time, so it
// must exceed the retry budget or the loop is cut short — see httpTimeout.
func NewColdStartRetryTransport(inner http.RoundTripper, retry ColdStartRetry) http.RoundTripper {
	return &coldStartRetryTransport{inner: inner, retry: retry}
}

type coldStartRetryTransport struct {
	inner http.RoundTripper
	retry ColdStartRetry
}

func (t *coldStartRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the body up front so a failed cold-start attempt can be replayed;
	// MCP request bodies are small JSON-RPC messages, so this is cheap. A nil
	// body (e.g. GET) stays nil and needs no replay.
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("buffer request body for retry: %w", err)
		}
		body = b
	}

	backoff := 250 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= t.retry.MaxAttempts; attempt++ {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := t.inner.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if req.Context().Err() != nil {
			// The caller gave up (its deadline or cancellation); stop retrying and
			// report why rather than the transport's derived error.
			return nil, req.Context().Err()
		}
		if !isColdStartError(err) || attempt == t.retry.MaxAttempts {
			return nil, err
		}

		slog.Warn("remote mcp upstream not ready, retrying",
			"attempt", attempt, "max", t.retry.MaxAttempts, "err", err)
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(backoff):
		}
		if backoff < t.retry.BackoffCap {
			backoff *= 2
		}
	}
	return nil, lastErr
}

// --- helpers ---

// isColdStartError reports whether err is a transport-level connection failure
// consistent with an upstream that is still waking from autostop — safe to
// retry because no response was produced. It deliberately whitelists only
// recognized connection errors (reset / refused / EOF / dial timeout) so that a
// deterministic failure like a TLS pin mismatch, which surfaces as a cert error
// rather than a connection error, is treated as terminal and reported at once.
func isColdStartError(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		// A dial/read/write failure while the machine boots — including a
		// timed-out connect — means the request never reached the upstream.
		return true
	}
	return false
}
