package agent

import (
	"strings"
	"testing"

	"tclaw/internal/channel"
)

func TestValidSetupToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"valid long token", "sk-ant-oat01-" + strings.Repeat("a", 50), true},
		{"too short", "sk-ant-oat01-abc", false},
		{"empty", "", false},
		{"contains spaces", "sk-ant-oat01-" + strings.Repeat("a", 40) + " extra", false},
		{"contains newline", "sk-ant-oat01-" + strings.Repeat("a", 40) + "\n", false},
		{"valid chars only", strings.Repeat("abcABC012-_", 10), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidSetupToken(tt.token); got != tt.want {
				t.Errorf("ValidSetupToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

func TestExtractSetupToken_ValidatesFormat(t *testing.T) {
	// A token that's too short should be rejected even if the prefix matches.
	output := "Your token:\nsk-ant-oat01-short\nDone."
	if got := ExtractSetupToken(output); got != "" {
		t.Errorf("expected empty for short token, got %q", got)
	}

	// A valid-length token should be extracted.
	validToken := "sk-ant-oat01-" + strings.Repeat("x", 50)
	output = "Your token:\n" + validToken + "\nDone."
	if got := ExtractSetupToken(output); got != validToken {
		t.Errorf("expected %q, got %q", validToken, got)
	}
}

func TestHandleAPIKeyEntry_RejectsShortKeys(t *testing.T) {
	// A key with correct prefix but too short should be rejected.
	ch := &mockChannel{}
	chID := channel.ChannelID("test")
	opts := Options{Channels: map[channel.ChannelID]channel.Channel{chID: ch}}
	ok := handleAPIKeyEntry(nil, opts, ch, chID, "sk-ant-short")
	if ok {
		t.Error("expected short API key to be rejected")
	}
	if len(ch.sends) == 0 {
		t.Error("expected error message to be sent")
	}
}
