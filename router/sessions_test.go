package router

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/store"
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

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return s
}

func TestChatID_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	save := saveChatIDFunc(s, "mybot")
	save(int64(987654321))

	got := loadChatID(context.Background(), s, "mybot")
	require.Equal(t, int64(987654321), got)
}

func TestChatID_EmptyStoreReturnsZero(t *testing.T) {
	s := newTestStore(t)
	got := loadChatID(context.Background(), s, "nonexistent")
	require.Equal(t, int64(0), got)
}

func TestChatID_CorruptedDataReturnsZero(t *testing.T) {
	s := newTestStore(t)

	// Write 3 bytes — loadChatID expects exactly 8.
	err := s.Set(context.Background(), "chatid_broken", []byte{0x01, 0x02, 0x03})
	require.NoError(t, err)

	got := loadChatID(context.Background(), s, "broken")
	require.Equal(t, int64(0), got)
}

func TestChatID_NegativeValue(t *testing.T) {
	s := newTestStore(t)
	save := saveChatIDFunc(s, "negchat")
	save(int64(-42))

	got := loadChatID(context.Background(), s, "negchat")
	require.Equal(t, int64(-42), got)
}

func TestChatID_OverwritesPrevious(t *testing.T) {
	s := newTestStore(t)
	save := saveChatIDFunc(s, "evolving")
	save(int64(111))
	save(int64(222))

	got := loadChatID(context.Background(), s, "evolving")
	require.Equal(t, int64(222), got)
}

func TestChatID_IndependentPerChannel(t *testing.T) {
	s := newTestStore(t)
	saveChatIDFunc(s, "alpha")(int64(100))
	saveChatIDFunc(s, "beta")(int64(200))

	require.Equal(t, int64(100), loadChatID(context.Background(), s, "alpha"))
	require.Equal(t, int64(200), loadChatID(context.Background(), s, "beta"))
}

func TestChatID_ConcurrentSafe(t *testing.T) {
	s := newTestStore(t)
	save := saveChatIDFunc(s, "concurrent")

	// Write from multiple goroutines — should not panic or corrupt.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			save(id)
		}(int64(i))
	}
	wg.Wait()

	got := loadChatID(context.Background(), s, "concurrent")
	require.True(t, got >= 0 && got <= 9, "expected value 0-9, got %d", got)
}
