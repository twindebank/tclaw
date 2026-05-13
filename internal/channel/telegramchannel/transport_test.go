package telegramchannel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/stretchr/testify/require"
)

func TestTelegram_ChatIDSeededFromOpts(t *testing.T) {
	tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{
		ChatID: 42,
	})
	require.Equal(t, int64(42), tg.currentChatID)
}

func TestTelegram_ChatIDDefaultsToZero(t *testing.T) {
	tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{})
	require.Equal(t, int64(0), tg.currentChatID)
}

func TestRegisterWebhook(t *testing.T) {
	t.Run("retries past 429 with retry_after and eventually succeeds", func(t *testing.T) {
		var setWebhookCalls atomic.Int32
		server := newFakeTelegramServer(func(w http.ResponseWriter, r *http.Request) {
			if setWebhookCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		})
		defer server.Close()

		tg := newTelegramForTest()
		b := newBotForTest(t, server.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		require.True(t, tg.registerWebhook(ctx, b))
		require.GreaterOrEqual(t, setWebhookCalls.Load(), int32(2), "expected at least one retry after 429")
	})

	t.Run("aborts the loop when context is cancelled mid-retry", func(t *testing.T) {
		// A persistent 429 with a long retry_after would otherwise pin the
		// goroutine for the full backoff. Cancel mid-wait and verify we exit
		// promptly with a failure result instead of hanging.
		var setWebhookCalls atomic.Int32
		server := newFakeTelegramServer(func(w http.ResponseWriter, r *http.Request) {
			setWebhookCalls.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":60}}`))
		})
		defer server.Close()

		tg := newTelegramForTest()
		b := newBotForTest(t, server.URL)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		require.False(t, tg.registerWebhook(ctx, b))
		require.Less(t, time.Since(start), 5*time.Second, "should exit shortly after context cancel, not wait the full retry_after")
		require.GreaterOrEqual(t, setWebhookCalls.Load(), int32(1))
	})

	t.Run("non-rate-limit errors fail immediately without retry", func(t *testing.T) {
		var setWebhookCalls atomic.Int32
		server := newFakeTelegramServer(func(w http.ResponseWriter, r *http.Request) {
			setWebhookCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: invalid webhook URL"}`))
		})
		defer server.Close()

		tg := newTelegramForTest()
		b := newBotForTest(t, server.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		require.False(t, tg.registerWebhook(ctx, b))
		require.Equal(t, int32(1), setWebhookCalls.Load(), "non-429 errors should bail out without retrying")
	})
}

// newFakeTelegramServer stubs the bot API. bot.New issues a getMe to verify
// the token; route that to a canned response and forward setWebhook to the
// caller-supplied handler so tests stay focused on the path under test.
func newFakeTelegramServer(setWebhook http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"fakebot"}}`))
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			setWebhook(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newBotForTest(t *testing.T, serverURL string) *bot.Bot {
	t.Helper()
	b, err := bot.New("fake-token", bot.WithServerURL(serverURL))
	require.NoError(t, err)
	return b
}

func newTelegramForTest() *Telegram {
	return NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{
		WebhookURL:      "https://example.com/telegram/test",
		WebhookPath:     "/telegram/test",
		RegisterHandler: func(string, http.Handler) {},
	})
}
