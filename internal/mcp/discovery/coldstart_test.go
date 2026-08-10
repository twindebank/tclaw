package discovery_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp/discovery"
)

func TestColdStartRetryTransport(t *testing.T) {
	fastRetry := discovery.ColdStartRetry{MaxAttempts: 4, BackoffCap: 5 * time.Millisecond}

	t.Run("retries a connection reset until the upstream wakes", func(t *testing.T) {
		stub := &stubRoundTripper{failures: 2, err: io.EOF, status: http.StatusOK}
		transport := discovery.NewColdStartRetryTransport(stub, fastRetry)

		resp, err := transport.RoundTrip(newRequest(t, "body-to-replay"))

		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, int32(3), stub.attempts.Load())
		// The buffered body must survive replay, or the winning attempt sends nothing.
		require.Equal(t, "body-to-replay", stub.lastBody())
	})

	t.Run("gives up once the attempt budget is spent", func(t *testing.T) {
		stub := &stubRoundTripper{failures: 99, err: io.EOF}
		transport := discovery.NewColdStartRetryTransport(stub, fastRetry)

		resp, err := transport.RoundTrip(newRequest(t, "never-delivered"))

		require.Error(t, err)
		require.Nil(t, resp)
		require.Equal(t, int32(fastRetry.MaxAttempts), stub.attempts.Load())
	})

	t.Run("passes a real HTTP error response straight through", func(t *testing.T) {
		// A 500 from a live upstream is an answer, not a cold start — retrying it
		// would turn one bad response into several.
		stub := &stubRoundTripper{status: http.StatusInternalServerError}
		transport := discovery.NewColdStartRetryTransport(stub, fastRetry)

		resp, err := transport.RoundTrip(newRequest(t, ""))

		require.NoError(t, err)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.Equal(t, int32(1), stub.attempts.Load())
	})

	t.Run("stops retrying when the caller cancels", func(t *testing.T) {
		stub := &stubRoundTripper{failures: 99, err: io.EOF}
		transport := discovery.NewColdStartRetryTransport(stub, discovery.ColdStartRetry{
			MaxAttempts: 50,
			BackoffCap:  time.Second,
		})

		ctx, cancel := context.WithCancel(context.Background())
		req := newRequest(t, "").WithContext(ctx)
		cancel()

		resp, err := transport.RoundTrip(req)

		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, resp)
	})

	t.Run("default budget outlasts a browser-booting cold start", func(t *testing.T) {
		// understudy measured ~21s from the waking request to its port accepting,
		// so a budget at or under that reintroduces the bug this replaced.
		backoff, total := 250*time.Millisecond, time.Duration(0)
		for attempt := 1; attempt < discovery.DefaultColdStartRetry.MaxAttempts; attempt++ {
			total += backoff
			if backoff < discovery.DefaultColdStartRetry.BackoffCap {
				backoff *= 2
			}
		}

		require.Greater(t, total, 30*time.Second)
	})
}

// --- helpers ---

func newRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/mcp", strings.NewReader(body))
	require.NoError(t, err)
	return req
}

// stubRoundTripper fails its first `failures` attempts with err, then answers
// with status, recording what it was sent.
type stubRoundTripper struct {
	failures int
	err      error
	status   int

	attempts atomic.Int32
	body     atomic.Value
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	attempt := s.attempts.Add(1)
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		s.body.Store(string(b))
	}
	if int(attempt) <= s.failures {
		return nil, s.err
	}
	return &http.Response{StatusCode: s.status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (s *stubRoundTripper) lastBody() string {
	v, _ := s.body.Load().(string)
	return v
}
