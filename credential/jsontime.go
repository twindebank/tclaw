package credential

import (
	"encoding/json"
	"time"
)

// jsonTime wraps time.Time to handle multiple JSON time formats during
// migration from legacy connection credentials.
type jsonTime struct {
	time.Time
}

func (t *jsonTime) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		// Not a string — might be null or a number.
		return nil
	}
	if s == "" {
		return nil
	}

	// Try RFC3339 first (most common).
	parsed, err := time.Parse(time.RFC3339, s)
	if err == nil {
		t.Time = parsed
		return nil
	}

	// Try RFC3339Nano.
	parsed, err = time.Parse(time.RFC3339Nano, s)
	if err == nil {
		t.Time = parsed
		return nil
	}

	// Give up — zero time is fine, token refresh will handle it.
	return nil
}
