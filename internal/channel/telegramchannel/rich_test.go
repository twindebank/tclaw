package telegramchannel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestTelegram_SendRich(t *testing.T) {
	t.Run("a reply goes out as rich markdown and its edits stay rich", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		id, err := tg.Send(context.Background(), "| a | b |\n|---|---|\n| 1 | 2 |", channel.SendOpts{Notify: true, Rich: true})
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

		_, err := tg.Send(context.Background(), "<broken", channel.SendOpts{Notify: true, Rich: true})
		require.NoError(t, err)

		calls := api.Calls()
		require.Equal(t, []string{"sendRichMessage", "sendMessage"}, methods(calls))
		require.Equal(t, "⚠️ [formatting error — sent as plain text]\n\n<broken", calls[1].Form["text"])
		require.Empty(t, calls[1].Form["parse_mode"], "the fallback must not be parsed at all")
	})

	t.Run("any other refusal is resent as plain text too", func(t *testing.T) {
		api := newRecordingAPI(t, map[string]fakeFailure{
			"sendRichMessage": {Status: http.StatusNotFound, Body: `{"ok":false,"error_code":404,"description":"Not Found: method not found"}`},
		})
		tg := newTelegramWithAPI(t, api)

		_, err := tg.Send(context.Background(), "hello", channel.SendOpts{Notify: true, Rich: true})
		require.NoError(t, err)
		require.Equal(t, []string{"sendRichMessage", "sendMessage"}, methods(api.Calls()))
	})

	t.Run("an edit Telegram refuses is made as plain text", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)
		id, err := tg.Send(context.Background(), "start", channel.SendOpts{Notify: true, Rich: true})
		require.NoError(t, err)
		api.Fail("editMessageText", fakeFailure{Status: http.StatusBadRequest, Body: `{"ok":false,"error_code":400,"description":"Bad Request: can't parse rich message"}`}, 1)

		require.NoError(t, tg.Edit(context.Background(), id, "<broken"))

		calls := api.Calls()
		require.Equal(t, []string{"sendRichMessage", "editMessageText", "editMessageText"}, methods(calls))
		require.Equal(t, "⚠️ [formatting error — sent as plain text]\n\n<broken", calls[2].Form["text"])
	})

	t.Run("an edit that changes nothing is not resent", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)
		id, err := tg.Send(context.Background(), "same", channel.SendOpts{Notify: true, Rich: true})
		require.NoError(t, err)
		api.Fail("editMessageText", fakeFailure{Status: http.StatusBadRequest, Body: `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`}, 1)

		err = tg.Edit(context.Background(), id, "same")
		require.Error(t, err)
		require.Contains(t, err.Error(), "message is not modified", "the caller treats this as success")
		require.Equal(t, []string{"sendRichMessage", "editMessageText"}, methods(api.Calls()))
	})

	t.Run("buttons the agent wrote are shown as text", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		_, err := tg.Send(context.Background(), `Tap <tg-button type="callback_data" data="x">here</tg-button>`, channel.SendOpts{Notify: true, Rich: true})
		require.NoError(t, err)

		require.JSONEq(t, `{"markdown":"Tap &lt;tg-button type=\"callback_data\" data=\"x\">here&lt;/tg-button>"}`,
			api.Calls()[0].Form["rich_message"])
	})

	t.Run("a rate limit is returned, not resent as plain text", func(t *testing.T) {
		api := newRecordingAPI(t, map[string]fakeFailure{
			"sendRichMessage": {Status: http.StatusTooManyRequests, Body: `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`},
		})
		tg := newTelegramWithAPI(t, api)

		_, err := tg.Send(context.Background(), "hello", channel.SendOpts{Notify: true, Rich: true})
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

	mu       sync.Mutex
	calls    []apiCall
	failures map[string]fakeFailure

	// failuresLeft counts down a failure set with Fail; an absent entry fails forever.
	failuresLeft map[string]int
}

func newRecordingAPI(t *testing.T, failures map[string]fakeFailure) *recordingAPI {
	t.Helper()
	if failures == nil {
		failures = map[string]fakeFailure{}
	}
	api := &recordingAPI{failures: failures, failuresLeft: map[string]int{}}
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
		failure, fails := api.failures[method]
		if left, counted := api.failuresLeft[method]; counted {
			if left == 1 {
				delete(api.failures, method)
				delete(api.failuresLeft, method)
			} else {
				api.failuresLeft[method] = left - 1
			}
		}
		api.mu.Unlock()

		if fails {
			w.WriteHeader(failure.Status)
			_, _ = w.Write([]byte(failure.Body))
			return
		}
		if strings.HasSuffix(method, "Draft") {
			// A draft is not a message; Telegram answers with true.
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7,"date":0,"chat":{"id":42,"type":"private"}}}`))
	}))
	t.Cleanup(api.server.Close)
	return api
}

// Fail makes the next times calls to method answer with failure.
func (a *recordingAPI) Fail(method string, failure fakeFailure, times int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[method] = failure
	a.failuresLeft[method] = times
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

func TestTelegram_StreamDraft(t *testing.T) {
	t.Run("sends the text as a stoppable rich draft", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		require.NoError(t, tg.StreamDraft(context.Background(), channel.StreamDraftParams{DraftID: 99, Text: "Half a rep"}))

		calls := api.Calls()
		require.Equal(t, []string{"sendRichMessageDraft"}, methods(calls))
		require.Equal(t, "99", calls[0].Form["draft_id"], "Telegram documents draft_id as an integer")
		require.Equal(t, "true", calls[0].Form["can_stop"])
		require.JSONEq(t, `{"markdown":"Half a rep"}`, calls[0].Form["rich_message"])
	})

	t.Run("reports a draft Telegram refuses", func(t *testing.T) {
		api := newRecordingAPI(t, map[string]fakeFailure{
			"sendRichMessageDraft": {Status: http.StatusBadRequest, Body: `{"ok":false,"error_code":400,"description":"Bad Request: drafts are not allowed"}`},
		})
		tg := newTelegramWithAPI(t, api)

		err := tg.StreamDraft(context.Background(), channel.StreamDraftParams{DraftID: 99, Text: "x"})
		require.Error(t, err)
	})
}
