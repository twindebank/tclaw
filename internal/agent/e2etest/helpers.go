package e2etest

import (
	"context"
	"testing"
	"time"
)

// ExtractPrompt finds the prompt text in CLI args. The prompt is the
// positional arg after "--" (not a named flag).
func ExtractPrompt(args []string) string {
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// RunWithTimeout runs the harness with a deadline and returns the result.
func RunWithTimeout(t *testing.T, h *Harness, timeout time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.Run(ctx)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
