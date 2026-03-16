package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTelegram_ChatIDSeededFromOpts(t *testing.T) {
	tg := newTelegram("fake-token", "test", "desc", []int64{1}, SourceStatic, TelegramOptions{
		ChatID: 42,
	})
	require.Equal(t, int64(42), tg.currentChatID)
}

func TestTelegram_ChatIDDefaultsToZero(t *testing.T) {
	tg := newTelegram("fake-token", "test", "desc", []int64{1}, SourceStatic, TelegramOptions{})
	require.Equal(t, int64(0), tg.currentChatID)
}

func TestTruncateReplySnippet(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short text unchanged", "hello world", 100, "hello world"},
		{"exact length not truncated", "12345", 5, "12345"},
		{"long text truncated with ellipsis", "abcdefghijklmnopqrstuvwxyz", 10, "abcdefghij…"},
		{"newlines collapsed to spaces", "line one\nline two", 100, "line one line two"},
		{"newlines collapsed before truncation", "aaa\nbbb\nccc", 5, "aaa b…"},
		{"empty string", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, truncateReplySnippet(tt.input, tt.maxLen))
		})
	}
}
