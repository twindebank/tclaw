package telegramchannel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestTelegram_SendRich(t *testing.T) {
	t.Run("a reply goes out as rich markdown and its edits stay rich", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		id, err := tg.Send(context.Background(), "| a | b |\n|---|---|\n| 1 | 2 |", channel.SendOpts{Notify: true})
		require.NoError(t, err)
		require.NoError(t, tg.Edit(context.Background(), id, "## Done"))

		calls := api.Calls()
		require.Equal(t, []string{"sendRichMessage", "editMessageText"}, methods(calls))
		require.JSONEq(t, `{"markdown":"| a | b |\n|---|---|\n| 1 | 2 |"}`, calls[0].Form["rich_message"])
		require.JSONEq(t, `{"markdown":"## Done"}`, calls[1].Form["rich_message"])
	})

	t.Run("a status message stays on the HTML path", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		id, err := tg.Send(context.Background(), "🔧 Bash(command=ls)", channel.SendOpts{})
		require.NoError(t, err)
		require.NoError(t, tg.Edit(context.Background(), id, "🔧 Bash(command=ls)\n  ↳ Done"))

		calls := api.Calls()
		require.Equal(t, []string{"sendMessage", "editMessageText"}, methods(calls))
		require.Equal(t, "HTML", calls[0].Form["parse_mode"])
		require.Empty(t, calls[1].Form["rich_message"])
	})

	t.Run("markup Telegram rejects is resent as plain text", func(t *testing.T) {
		api := newRecordingAPI(t, map[string]fakeFailure{
			"sendRichMessage": {Status: http.StatusBadRequest, Body: `{"ok":false,"error_code":400,"description":"Bad Request: can't parse rich message"}`},
		})
		tg := newTelegramWithAPI(t, api)

		_, err := tg.Send(context.Background(), "<broken", channel.SendOpts{Notify: true})
		require.NoError(t, err)

		calls := api.Calls()
		require.Equal(t, []string{"sendRichMessage", "sendMessage"}, methods(calls))
		require.Equal(t, "<broken", calls[1].Form["text"])
		require.Empty(t, calls[1].Form["parse_mode"], "the fallback must not be parsed at all")
	})

	t.Run("a rate limit is returned, not resent as plain text", func(t *testing.T) {
		api := newRecordingAPI(t, map[string]fakeFailure{
			"sendRichMessage": {Status: http.StatusTooManyRequests, Body: `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`},
		})
		tg := newTelegramWithAPI(t, api)

		_, err := tg.Send(context.Background(), "hello", channel.SendOpts{Notify: true})
		require.Error(t, err)
		require.Equal(t, []string{"sendRichMessage"}, methods(api.Calls()))
	})
}

// --- helpers ---

// fakeFailure is the answer the fake API gives to one method.
type fakeFailure struct {
	Status int
	Body   string
}

type apiCall struct {
	Method string
	Form   map[string]string
}

// recordingAPI fakes the Bot API, recording each call's method and form fields. Methods named in
// failures answer with that body; everything else succeeds with a message.
type recordingAPI struct {
	server *httptest.Server

	mu    sync.Mutex
	calls []apiCall
}

func newRecordingAPI(t *testing.T, failures map[string]fakeFailure) *recordingAPI {
	t.Helper()
	api := &recordingAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := path.Base(r.URL.Path)
		if method == "getMe" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"fakebot"}}`))
			return
		}
		require.NoError(t, r.ParseMultipartForm(1<<20))
		form := make(map[string]string)
		for k, v := range r.MultipartForm.Value {
			form[k] = v[0]
		}
		api.mu.Lock()
		api.calls = append(api.calls, apiCall{Method: method, Form: form})
		api.mu.Unlock()

		if failure, ok := failures[method]; ok {
			w.WriteHeader(failure.Status)
			_, _ = w.Write([]byte(failure.Body))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7,"date":0,"chat":{"id":42,"type":"private"}}}`))
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *recordingAPI) Calls() []apiCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]apiCall(nil), a.calls...)
}

func methods(calls []apiCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Method)
	}
	return out
}

func newTelegramWithAPI(t *testing.T, api *recordingAPI) *Telegram {
	t.Helper()
	tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{ChatID: 42})
	tg.bot = newBotForTest(t, api.server.URL)
	return tg
}
