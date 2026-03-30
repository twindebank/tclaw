package telegramchannel

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/store"
)

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

// --- helpers ---

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return s
}
