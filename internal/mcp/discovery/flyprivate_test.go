package discovery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsFlyPrivateHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"flycast name", "svc.flycast", true},
		{"internal name", "svc.internal", true},
		{"uppercase", "Svc.FLYCAST", true},
		{"trailing dot", "svc.flycast.", true},
		{"public domain", "mcp.example.com", false},
		{"lookalike suffix", "flycast.evil.com", false},
		{"ip literal", "10.0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsFlyPrivateHost(tt.host))
		})
	}
}
