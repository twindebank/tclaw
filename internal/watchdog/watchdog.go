// Package watchdog restarts a wedged tclaw process. Fly's health check detects an
// unresponsive machine but only stops routing to it — it never restarts it — so a
// process that stops serving its own /healthz (goroutine starvation, deadlock, fd
// or memory exhaustion) stays wedged until a human intervenes, as happened in prod
// where the machine sat critical for hours. The watchdog probes the same health
// endpoint Fly does and, after sustained failure, exits non-zero so Fly's machine
// restart policy replaces the wedged process. tclaw already recovers cleanly on
// restart (persisted queue/outbox, auto-resumed channels), so a fast self-restart
// turns a multi-hour outage into a ~10s blip.
package watchdog

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"
)

// Config controls the liveness watchdog. Zero-value fields fall back to the
// defaults applied in Run.
type Config struct {
	// HealthURL is the local health endpoint to probe, e.g.
	// "http://127.0.0.1:9876/healthz". Required.
	HealthURL string

	// Interval between probes.
	Interval time.Duration

	// Timeout per probe. Kept at or slightly above Fly's own check timeout so the
	// watchdog and Fly agree on what "unresponsive" means.
	Timeout time.Duration

	// FailureThreshold is the number of consecutive failed probes that triggers a
	// restart. Chosen high enough that a transient blip never restarts a healthy
	// process.
	FailureThreshold int

	// GracePeriod delays the first probe so a slow boot isn't counted as a
	// failure.
	GracePeriod time.Duration

	// onWedged runs when the failure threshold is reached. Defaults to a non-zero
	// os.Exit so Fly restarts the machine; tests override it.
	onWedged func()
}

func (c *Config) applyDefaults() {
	if c.Interval == 0 {
		c.Interval = 30 * time.Second
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	if c.FailureThreshold == 0 {
		c.FailureThreshold = 4
	}
	if c.GracePeriod == 0 {
		c.GracePeriod = 90 * time.Second
	}
	if c.onWedged == nil {
		c.onWedged = func() {
			// Non-zero exit so Fly's restart policy replaces the process. Deferred
			// cleanups are intentionally skipped — the process is wedged and
			// persisted state (queue/outbox) already survives a hard restart.
			os.Exit(1)
		}
	}
}

// Run probes HealthURL until ctx is cancelled, restarting the process after
// FailureThreshold consecutive failures. It blocks, so callers run it in a
// goroutine.
func Run(ctx context.Context, cfg Config) {
	cfg.applyDefaults()

	select {
	case <-ctx.Done():
		return
	case <-time.After(cfg.GracePeriod):
	}

	client := &http.Client{Timeout: cfg.Timeout}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	consecutiveFailures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := probe(ctx, client, cfg.HealthURL); err != nil {
				consecutiveFailures++
				slog.Warn("watchdog: health probe failed",
					"consecutive", consecutiveFailures, "threshold", cfg.FailureThreshold, "err", err)
				if consecutiveFailures >= cfg.FailureThreshold {
					slog.Error("watchdog: process wedged, restarting",
						"consecutive_failures", consecutiveFailures, "goroutines", runtime.NumGoroutine())
					cfg.onWedged()
					return
				}
				continue
			}
			if consecutiveFailures > 0 {
				slog.Info("watchdog: health probe recovered", "after_failures", consecutiveFailures)
			}
			consecutiveFailures = 0
		}
	}
}

// probe performs one health request, returning an error if the endpoint is
// unreachable or does not answer 200 within the client's timeout.
func probe(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build probe request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe returned status %d", resp.StatusCode)
	}
	return nil
}
