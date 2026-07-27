package watchdog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Run("restarts after threshold consecutive failures", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)

		wedged := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go Run(ctx, Config{
			HealthURL:        srv.URL + "/healthz",
			Interval:         5 * time.Millisecond,
			Timeout:          100 * time.Millisecond,
			FailureThreshold: 3,
			GracePeriod:      time.Millisecond,
			onWedged:         func() { wedged <- struct{}{} },
		})

		select {
		case <-wedged:
			// restart triggered as expected
		case <-time.After(2 * time.Second):
			require.Fail(t, "watchdog did not restart after sustained health failures")
		}
	})

	t.Run("stays alive while healthy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		var restarts atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			Run(ctx, Config{
				HealthURL:        srv.URL + "/healthz",
				Interval:         5 * time.Millisecond,
				Timeout:          100 * time.Millisecond,
				FailureThreshold: 3,
				GracePeriod:      time.Millisecond,
				onWedged:         func() { restarts.Add(1) },
			})
			close(done)
		}()

		time.Sleep(200 * time.Millisecond)
		cancel()
		<-done

		require.Zero(t, restarts.Load(), "healthy process must never be restarted")
	})

	t.Run("recovery before threshold does not restart", func(t *testing.T) {
		var healthy atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if healthy.Load() {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)

		var restarts atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			Run(ctx, Config{
				HealthURL:        srv.URL + "/healthz",
				Interval:         5 * time.Millisecond,
				Timeout:          100 * time.Millisecond,
				FailureThreshold: 5,
				GracePeriod:      time.Millisecond,
				onWedged:         func() { restarts.Add(1) },
			})
			close(done)
		}()

		// A brief failing window (below threshold) followed by recovery must not
		// trip a restart — the consecutive counter has to reset on the first 200.
		time.Sleep(15 * time.Millisecond)
		healthy.Store(true)
		time.Sleep(200 * time.Millisecond)
		cancel()
		<-done

		require.Zero(t, restarts.Load(), "recovered process must not be restarted")
	})
}

func TestProbe(t *testing.T) {
	t.Run("nil error on 200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		require.NoError(t, probe(context.Background(), &http.Client{Timeout: time.Second}, srv.URL))
	})

	t.Run("error on non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(srv.Close)

		err := probe(context.Background(), &http.Client{Timeout: time.Second}, srv.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "503")
	})

	t.Run("error when unreachable", func(t *testing.T) {
		// A port nothing listens on — the probe must surface the dial failure.
		err := probe(context.Background(), &http.Client{Timeout: 200 * time.Millisecond}, "http://127.0.0.1:1/healthz")
		require.Error(t, err)
	})
}
