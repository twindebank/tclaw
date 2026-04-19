package secretform

import "time"

// SetMaxWaitPerCall overrides the per-call wait window for tests. Returns a
// restore function. Not exported outside of tests.
func SetMaxWaitPerCall(d time.Duration) func() {
	prev := maxWaitPerCall
	maxWaitPerCall = d
	return func() { maxWaitPerCall = prev }
}
