package devtools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"4d", 4 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"2w", 2 * 7 * 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"90m", 90 * time.Minute},
		{"30s", 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := parseDuration(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.expected, d)
		})
	}
}

func TestParseDuration_Invalid(t *testing.T) {
	cases := []string{"", "abc", "0d", "-1d", "1x", "d"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			_, err := parseDuration(s)
			require.Error(t, err)
		})
	}
}
