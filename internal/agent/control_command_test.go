package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsControlCommand(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "stop", text: "stop", want: true},
		{name: "login", text: "login", want: true},
		{name: "auth", text: "auth", want: true},
		{name: "compact", text: "compact", want: true},
		{name: "fresh session new", text: "new", want: true},
		{name: "fresh session synonym reset", text: "reset", want: true},
		{name: "fresh session synonym clear", text: "clear", want: true},
		{name: "fresh session synonym delete", text: "delete", want: true},
		{name: "case-insensitive", text: "STOP", want: true},
		{name: "surrounding whitespace trimmed", text: "  stop  ", want: true},
		{name: "ordinary message is not control", text: "stop the presses", want: false},
		{name: "empty is not control", text: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsControlCommand(tc.text))
		})
	}
}
