package telegramchannel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{name: "menu command", text: "/stop", expected: "stop"},
		{name: "menu command addressed to the bot", text: "/compact@example_bot", expected: "compact"},
		{name: "menu command in another case", text: "/New", expected: "new"},
		{name: "unknown command is left alone", text: "/deploy", expected: "/deploy"},
		{name: "command with more words is left alone", text: "/stop the build please", expected: "/stop the build please"},
		{name: "plain text is left alone", text: "stop", expected: "stop"},
		{name: "a path is left alone", text: "/usr/local/bin", expected: "/usr/local/bin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, normalizeCommand(tt.text))
		})
	}
}
