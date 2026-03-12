package router

import (
	"strings"
	"testing"
)

func TestValidSessionID(t *testing.T) {
	tests := []struct {
		name string
		sid  string
		want bool
	}{
		{"valid uuid", "a78251df-ddfd-49cd-8ae3-cf9007044ae1", true},
		{"valid short", "abc123", true},
		{"empty", "", false},
		{"too long", strings.Repeat("x", 257), false},
		{"max length", strings.Repeat("x", 256), true},
		{"contains null byte", "abc\x00def", false},
		{"contains newline", "abc\ndef", false},
		{"contains tab", "abc\tdef", false},
		{"contains DEL", "abc\x7fdef", false},
		{"printable special chars", "abc-def_123.foo", true},
		{"spaces allowed", "session with spaces", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSessionID(tt.sid); got != tt.want {
				t.Errorf("validSessionID(%q) = %v, want %v", tt.sid, got, tt.want)
			}
		})
	}
}
