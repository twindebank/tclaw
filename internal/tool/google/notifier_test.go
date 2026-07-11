package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/libraries/store"
	"tclaw/internal/notification"
)

var errTest = errors.New("boom")

func emptyDepsMap() map[credential.CredentialSetID]Deps { return nil }

func TestNotifier_NotificationTypes(t *testing.T) {
	t.Run("declares new_email with correct scopes", func(t *testing.T) {
		n, _ := setupNotifier(t)
		types := n.NotificationTypes()

		require.Len(t, types, 1)
		require.Equal(t, TypeNewEmail, types[0].Name)
		require.Contains(t, types[0].Scopes, notification.ScopeCredential)
		require.Contains(t, types[0].Scopes, notification.ScopePersistent)
	})
}

func TestNotifier_Subscribe(t *testing.T) {
	t.Run("builds subscription with correct fields", func(t *testing.T) {
		n, _ := setupNotifier(t)

		result, err := n.Subscribe(context.Background(), notification.SubscribeParams{
			TypeName:        TypeNewEmail,
			ChannelName:     "phone",
			Scope:           notification.ScopeCredential,
			CredentialSetID: "google/work",
			Label:           "Work email notifications",
		}, &noopEmitter{})
		require.NoError(t, err)
		defer result.Cancel()

		sub := result.Subscription
		require.NotEmpty(t, sub.ID)
		require.Equal(t, notification.ScopeCredential, sub.Scope)
		require.Equal(t, "phone", sub.ChannelName)
		require.Equal(t, "google", sub.PackageName)
		require.Equal(t, TypeNewEmail, sub.TypeName)
		require.Equal(t, "google/work", sub.CredentialSetID)

		var config gmailPollConfig
		require.NoError(t, json.Unmarshal(sub.Config, &config))
		require.Equal(t, "google/work", config.CredentialSetID)
		require.Equal(t, defaultPollInterval, config.Interval)
	})

	t.Run("rejects unknown notification type", func(t *testing.T) {
		n, _ := setupNotifier(t)
		_, err := n.Subscribe(context.Background(), notification.SubscribeParams{
			TypeName: "nonexistent",
		}, &noopEmitter{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown notification type")
	})
}

func TestNotifier_Cancel(t *testing.T) {
	t.Run("is idempotent and cleans up the cursor", func(t *testing.T) {
		n, s := setupNotifier(t)
		ctx := context.Background()

		result, err := n.Subscribe(ctx, notification.SubscribeParams{
			TypeName:        TypeNewEmail,
			ChannelName:     "main",
			Scope:           notification.ScopePersistent,
			CredentialSetID: "google/work",
		}, &noopEmitter{})
		require.NoError(t, err)

		require.NoError(t, s.Set(ctx, cursorKey(result.Subscription.ID), []byte("99999")))

		result.Cancel()
		result.Cancel()
		n.Cancel(result.Subscription.ID)

		data, err := s.Get(ctx, cursorKey(result.Subscription.ID))
		require.NoError(t, err)
		require.Empty(t, data)
	})

	t.Run("preserves seen set so sibling subscriptions keep dedupe state", func(t *testing.T) {
		// The seen set is shared across all subscriptions for a credential set,
		// so cancelling one subscription must not wipe it out.
		n, _ := setupNotifier(t)
		ctx := context.Background()

		result, err := n.Subscribe(ctx, notification.SubscribeParams{
			TypeName:        TypeNewEmail,
			ChannelName:     "main",
			Scope:           notification.ScopeCredential,
			CredentialSetID: "google/work",
		}, &noopEmitter{})
		require.NoError(t, err)

		n.saveSeen(ctx, "google/work", []string{"msg1", "msg2"})

		n.Cancel(result.Subscription.ID)

		require.Equal(t, []string{"msg1", "msg2"}, n.loadSeen(ctx, "google/work"))
	})
}

func TestNotifier_Resubscribe(t *testing.T) {
	t.Run("restarts from persisted config", func(t *testing.T) {
		n, _ := setupNotifier(t)

		result, err := n.Subscribe(context.Background(), notification.SubscribeParams{
			TypeName:        TypeNewEmail,
			ChannelName:     "phone",
			Scope:           notification.ScopeCredential,
			CredentialSetID: "google/work",
			Label:           "test",
		}, &noopEmitter{})
		require.NoError(t, err)
		result.Cancel()

		cancel, err := n.Resubscribe(context.Background(), result.Subscription, &noopEmitter{})
		require.NoError(t, err)
		require.NotNil(t, cancel)
		cancel()
	})

	t.Run("loads cursor from state store", func(t *testing.T) {
		n, s := setupNotifier(t)
		ctx := context.Background()

		config := gmailPollConfig{
			CredentialSetID: "google/work",
			Interval:        defaultPollInterval,
		}
		configJSON, err := json.Marshal(config)
		require.NoError(t, err)

		sub := notification.Subscription{
			ID:              notification.GenerateID(),
			Scope:           notification.ScopeCredential,
			ChannelName:     "phone",
			PackageName:     "google",
			TypeName:        TypeNewEmail,
			Config:          configJSON,
			CredentialSetID: "google/work",
		}

		// Simulate a cursor persisted by a previous session's poll loop.
		require.NoError(t, s.Set(ctx, cursorKey(sub.ID), []byte("persisted_cursor")))

		cancel, err := n.Resubscribe(ctx, sub, &noopEmitter{})
		require.NoError(t, err)
		require.NotNil(t, cancel)
		cancel()

		// The poll loop should have loaded the persisted cursor.
		require.Equal(t, "persisted_cursor", n.loadCursor(ctx, sub.ID))
	})
}

func TestNotifier_CursorPersistence(t *testing.T) {
	t.Run("save and load round-trip", func(t *testing.T) {
		n, _ := setupNotifier(t)
		ctx := context.Background()
		id := notification.GenerateID()

		n.saveCursor(ctx, id, "12345")
		require.Equal(t, "12345", n.loadCursor(ctx, id))

		n.saveCursor(ctx, id, "67890")
		require.Equal(t, "67890", n.loadCursor(ctx, id))
	})

	t.Run("load returns empty for missing key", func(t *testing.T) {
		n, _ := setupNotifier(t)
		require.Equal(t, "", n.loadCursor(context.Background(), "notif_nonexistent"))
	})
}

func TestNotifier_SeenPersistence(t *testing.T) {
	t.Run("save and load round-trip", func(t *testing.T) {
		n, _ := setupNotifier(t)
		ctx := context.Background()

		n.saveSeen(ctx, "google/work", []string{"msg1", "msg2", "msg3"})
		require.Equal(t, []string{"msg1", "msg2", "msg3"}, n.loadSeen(ctx, "google/work"))
	})

	t.Run("load returns nil for missing key", func(t *testing.T) {
		n, _ := setupNotifier(t)
		require.Nil(t, n.loadSeen(context.Background(), "google/missing"))
	})

	t.Run("load returns nil for corrupt data", func(t *testing.T) {
		n, s := setupNotifier(t)
		ctx := context.Background()

		require.NoError(t, s.Set(ctx, seenKey("google/work"), []byte("not valid json")))
		require.Nil(t, n.loadSeen(ctx, "google/work"))
	})

	t.Run("subscriptions sharing a credential set share dedupe state", func(t *testing.T) {
		n, _ := setupNotifier(t)
		ctx := context.Background()

		// Two distinct subscription IDs targeting the same credential set.
		credSet := "google/work"
		n.saveSeen(ctx, credSet, []string{"msgA"})

		// A second poller for a different subscription against the same
		// credential set sees the seen entries through the same key.
		require.Equal(t, []string{"msgA"}, n.loadSeen(ctx, credSet))
	})
}

func TestFilterSeen(t *testing.T) {
	t.Run("returns all candidates when seen is empty", func(t *testing.T) {
		fresh, dupes := filterSeen([]string{"a", "b", "c"}, nil)
		require.Equal(t, []string{"a", "b", "c"}, fresh)
		require.Equal(t, 0, dupes)
	})

	t.Run("filters out seen ids and preserves order", func(t *testing.T) {
		fresh, dupes := filterSeen([]string{"a", "b", "c", "d"}, []string{"b", "d"})
		require.Equal(t, []string{"a", "c"}, fresh)
		require.Equal(t, 2, dupes)
	})

	t.Run("returns empty slice when all candidates are seen", func(t *testing.T) {
		fresh, dupes := filterSeen([]string{"a", "b"}, []string{"a", "b", "c"})
		require.Empty(t, fresh)
		require.Equal(t, 2, dupes)
	})
}

func TestAppendCapped(t *testing.T) {
	t.Run("appends when under cap", func(t *testing.T) {
		result := appendCapped([]string{"a", "b"}, []string{"c"}, 10)
		require.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("trims oldest entries when over cap", func(t *testing.T) {
		result := appendCapped([]string{"a", "b", "c"}, []string{"d", "e"}, 3)
		require.Equal(t, []string{"c", "d", "e"}, result)
	})

	t.Run("trims input larger than cap", func(t *testing.T) {
		result := appendCapped(nil, []string{"a", "b", "c", "d"}, 2)
		require.Equal(t, []string{"c", "d"}, result)
	})
}

func TestFormatEmailNotification(t *testing.T) {
	t.Run("carries exact ids, preview, and file path", func(t *testing.T) {
		text := formatEmailNotification(gmailReadResponse{
			ID:       "msg123",
			ThreadID: "thread456",
			From:     "alice@example.com",
			Subject:  "Meeting tomorrow",
			Date:     "Mon, 6 Jul 2026 10:00:00 +0000",
			Body:     "Hi, can we meet at 3pm?",
		}, "/data/alice/memory/emails/msg123.md")

		require.Contains(t, text, "alice@example.com")
		require.Contains(t, text, "Meeting tomorrow")
		require.Contains(t, text, "gmail_message_id: msg123")
		require.Contains(t, text, "thread_id: thread456")
		require.Contains(t, text, "Hi, can we meet at 3pm?")
		require.Contains(t, text, "/data/alice/memory/emails/msg123.md")
		require.Contains(t, text, "Do not reverse-search Gmail")
	})

	t.Run("without a file path points to google_gmail_read", func(t *testing.T) {
		text := formatEmailNotification(gmailReadResponse{
			ID: "msg123", From: "a@b.com", Subject: "Hi", Body: "body",
		}, "")
		require.Contains(t, text, "google_gmail_read")
		require.NotContains(t, text, "saved to")
	})
}

func TestFormatDegradedNotification(t *testing.T) {
	t.Run("includes message id and read instruction", func(t *testing.T) {
		text := formatDegradedNotification("msg789", errTest)
		require.Contains(t, text, "gmail_message_id: msg789")
		require.Contains(t, text, "google_gmail_read")
	})
}

func TestTruncatePreview(t *testing.T) {
	t.Run("collapses whitespace and leaves short text intact", func(t *testing.T) {
		require.Equal(t, "hello world", truncatePreview("hello   \n world", 100))
	})

	t.Run("truncates long text with an ellipsis", func(t *testing.T) {
		out := truncatePreview("abcdefghij", 4)
		require.Equal(t, "abcd…", out)
	})
}

func TestWriteEmailBodyFile(t *testing.T) {
	t.Run("writes frontmatter and body, returns readable path", func(t *testing.T) {
		n, _ := setupNotifier(t)

		path, err := n.writeEmailBodyFile(gmailReadResponse{
			ID:        "msgABC",
			ThreadID:  "thr1",
			From:      "alice@example.com",
			To:        "alice@example.com",
			Subject:   `Re: budget: "Q3" plan`,
			Date:      "Mon, 6 Jul 2026 10:00:00 +0000",
			MessageID: "<abc@mail.example.com>",
			Body:      "Line one\nLine two",
		})
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		content := string(data)

		require.Contains(t, content, "gmail_message_id: \"msgABC\"")
		require.Contains(t, content, "thread_id: \"thr1\"")
		require.Contains(t, content, "message_id_header: \"<abc@mail.example.com>\"")
		// Colons and quotes in the subject are JSON-escaped so the frontmatter stays valid.
		require.Contains(t, content, `subject: "Re: budget: \"Q3\" plan"`)
		require.Contains(t, content, "Line one\nLine two")
	})
}

func TestNotifier_FetchHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("single page returns all ids and advances the cursor", func(t *testing.T) {
		n := stubbedNotifier(t, []string{
			`{"history":[
				{"messagesAdded":[{"message":{"id":"m1"}}]},
				{"messagesAdded":[{"message":{"id":"m2"}}]}
			],"historyId":"2000"}`,
		})

		ids, historyID, err := n.fetchHistory(ctx, testCredSet, "1000")
		require.NoError(t, err)
		require.Equal(t, []string{"m1", "m2"}, ids)
		require.Equal(t, "2000", historyID)
	})

	t.Run("deduplicates ids repeated across records", func(t *testing.T) {
		n := stubbedNotifier(t, []string{
			`{"history":[
				{"messagesAdded":[{"message":{"id":"m1"}},{"message":{"id":"m1"}}]},
				{"messagesAdded":[{"message":{"id":"m2"}}]}
			],"historyId":"2000"}`,
		})

		ids, _, err := n.fetchHistory(ctx, testCredSet, "1000")
		require.NoError(t, err)
		require.Equal(t, []string{"m1", "m2"}, ids)
	})

	t.Run("follows nextPageToken across pages so nothing is skipped", func(t *testing.T) {
		n := stubbedNotifier(t, []string{
			`{"history":[{"messagesAdded":[{"message":{"id":"m1"}},{"message":{"id":"m2"}}]}],"historyId":"2500","nextPageToken":"p2"}`,
			`{"history":[{"messagesAdded":[{"message":{"id":"m2"}},{"message":{"id":"m3"}}]}],"historyId":"3000"}`,
		})

		ids, historyID, err := n.fetchHistory(ctx, testCredSet, "1000")
		require.NoError(t, err)
		require.Equal(t, []string{"m1", "m2", "m3"}, ids)
		// Cursor comes from the terminal page, not an intermediate one.
		require.Equal(t, "3000", historyID)
	})

	t.Run("holds the cursor when pagination hits the page cap", func(t *testing.T) {
		// Every page keeps a nextPageToken so the loop is bounded only by
		// maxHistoryPages. The collected ids must still come back, but with an
		// empty historyId so the caller holds the cursor and re-pages next poll.
		pages := make([]string, maxHistoryPages)
		want := make([]string, maxHistoryPages)
		for i := range pages {
			id := fmt.Sprintf("m%d", i)
			want[i] = id
			pages[i] = fmt.Sprintf(`{"history":[{"messagesAdded":[{"message":{"id":%q}}]}],"historyId":"9999","nextPageToken":"p%d"}`, id, i+1)
		}
		n := stubbedNotifier(t, pages)

		ids, historyID, err := n.fetchHistory(ctx, testCredSet, "1000")
		require.NoError(t, err)
		require.Equal(t, want, ids)
		require.Empty(t, historyID, "capped pagination must hold the cursor")
	})
}

func TestIsNotFoundError(t *testing.T) {
	t.Run("matches Gmail notFound reason", func(t *testing.T) {
		err := errors.New(`get message: {"error":{"code":404,"message":"Requested entity was not found.","reason":"notFound"}}`)
		require.True(t, isNotFoundError(err))
	})

	t.Run("matches the human-readable phrasing", func(t *testing.T) {
		require.True(t, isNotFoundError(errors.New("get message: Requested entity was not found.")))
	})

	t.Run("does not match unrelated errors", func(t *testing.T) {
		require.False(t, isNotFoundError(errTest))
		require.False(t, isNotFoundError(errors.New("rate limit exceeded")))
	})

	t.Run("nil is not a not-found", func(t *testing.T) {
		require.False(t, isNotFoundError(nil))
	})
}

func TestNotifier_BuildNotification(t *testing.T) {
	ctx := context.Background()

	t.Run("returns a full notification for a present message", func(t *testing.T) {
		n := messageStubNotifier(t, map[string]messageStubResponse{
			"m1": {body: validMessageJSON("m1", "alice@example.com", "Hello there", "hi")},
		})

		text, outcome := n.buildNotification(ctx, Deps{}, "m1")
		require.Equal(t, fetchOK, outcome)
		require.Contains(t, text, "alice@example.com")
		require.Contains(t, text, "Hello there")
		require.Contains(t, text, "gmail_message_id: m1")
	})

	t.Run("returns fetchGone with no text for a deleted message", func(t *testing.T) {
		n := messageStubNotifier(t, map[string]messageStubResponse{
			"m1": {err: notFoundErr()},
		})

		text, outcome := n.buildNotification(ctx, Deps{}, "m1")
		require.Equal(t, fetchGone, outcome)
		require.Empty(t, text)
	})

	t.Run("degrades to an id-only notification on a transient error", func(t *testing.T) {
		n := messageStubNotifier(t, map[string]messageStubResponse{
			"m1": {err: errors.New("get message: connection reset")},
		})

		text, outcome := n.buildNotification(ctx, Deps{}, "m1")
		require.Equal(t, fetchTransient, outcome)
		require.Contains(t, text, "gmail_message_id: m1")
		require.Contains(t, text, "could not fetch")
	})
}

func TestNotifier_Poll(t *testing.T) {
	ctx := context.Background()

	t.Run("skips deleted messages and emits only present ones", func(t *testing.T) {
		em := &noopEmitter{}
		n := pollStubNotifier(t,
			`{"history":[{"messagesAdded":[{"message":{"id":"m1"}}]},{"messagesAdded":[{"message":{"id":"m2"}}]}],"historyId":"2000"}`,
			map[string]messageStubResponse{
				"m1": {err: notFoundErr()},
				"m2": {body: validMessageJSON("m2", "bob@example.com", "Real email", "body")},
			})

		id := notification.GenerateID()
		cursor := n.poll(ctx, id, testCredSet, "1000", em)

		// Only the present message is notified.
		require.Len(t, em.messages, 1)
		require.Contains(t, em.messages[0].Text, "gmail_message_id: m2")

		// Both IDs are reserved — the phantom so it's never reprocessed, the real
		// one so it isn't re-notified.
		require.ElementsMatch(t, []string{"m1", "m2"}, n.loadSeen(ctx, testCredSet))
		require.Equal(t, "2000", cursor)
	})

	t.Run("a re-read of the same window re-notifies nothing (idempotent across restarts)", func(t *testing.T) {
		em := &noopEmitter{}
		history := `{"history":[{"messagesAdded":[{"message":{"id":"m2"}}]}],"historyId":"2000"}`
		n := pollStubNotifier(t, history, map[string]messageStubResponse{
			"m2": {body: validMessageJSON("m2", "bob@example.com", "Real email", "body")},
		})

		id := notification.GenerateID()

		// First poll delivers the email and reserves it durably.
		n.poll(ctx, id, testCredSet, "1000", em)
		require.Len(t, em.messages, 1)

		// A restart re-reads the same inclusive history window from the same
		// cursor. Because the reservation was persisted per-message, nothing is
		// re-delivered.
		n.poll(ctx, id, testCredSet, "1000", em)
		require.Len(t, em.messages, 1, "the same email must not be notified twice")
	})
}

// --- helpers ---

const testCredSet = "google/work"

// messageStubResponse is a canned Gmail messages.get result: either a body or an error.
type messageStubResponse struct {
	body string
	err  error
}

func notFoundErr() error {
	return errors.New(`get message: {"error":{"code":404,"message":"Requested entity was not found.","reason":"notFound"}}`)
}

// validMessageJSON builds a minimal format=full Gmail message with a plain-text body.
func validMessageJSON(id, from, subject, body string) string {
	data := base64.URLEncoding.EncodeToString([]byte(body))
	return fmt.Sprintf(`{"id":%q,"threadId":"t-%s","payload":{"mimeType":"text/plain","headers":[{"name":"From","value":%q},{"name":"Subject","value":%q}],"body":{"data":%q}}}`,
		id, id, from, subject, data)
}

// messageStubNotifier serves messages.get calls from a fixed map keyed by message ID.
func messageStubNotifier(t *testing.T, messages map[string]messageStubResponse) *notifier {
	t.Helper()
	n, _ := setupNotifier(t)
	n.depsMap = func() map[credential.CredentialSetID]Deps {
		return map[credential.CredentialSetID]Deps{testCredSet: {}}
	}
	n.run = func(_ context.Context, _ Deps, cmd gws.Command) (json.RawMessage, error) {
		return serveMessageGet(cmd, messages)
	}
	return n
}

// pollStubNotifier serves history.list from a single canned page and messages.get
// from a per-ID map, so a full poll can be driven without the gws binary.
func pollStubNotifier(t *testing.T, history string, messages map[string]messageStubResponse) *notifier {
	t.Helper()
	n, _ := setupNotifier(t)
	n.depsMap = func() map[credential.CredentialSetID]Deps {
		return map[credential.CredentialSetID]Deps{testCredSet: {}}
	}
	n.run = func(_ context.Context, _ Deps, cmd gws.Command) (json.RawMessage, error) {
		if len(cmd.Args) >= 3 && cmd.Args[2] == "history" {
			return json.RawMessage(history), nil
		}
		return serveMessageGet(cmd, messages)
	}
	return n
}

// serveMessageGet resolves a messages.get command against the canned map.
func serveMessageGet(cmd gws.Command, messages map[string]messageStubResponse) (json.RawMessage, error) {
	if len(cmd.Args) < 4 || cmd.Args[3] != "get" {
		return nil, fmt.Errorf("unexpected gws command: %v", cmd.Args)
	}
	id, _ := cmd.Params["id"].(string)
	rsp, ok := messages[id]
	if !ok {
		return nil, fmt.Errorf("no stubbed response for message %q", id)
	}
	if rsp.err != nil {
		return nil, rsp.err
	}
	return json.RawMessage(rsp.body), nil
}

func setupNotifier(t *testing.T) (*notifier, store.Store) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return newNotifier(emptyDepsMap, s, t.TempDir()), s
}

// stubbedNotifier returns a notifier whose Gmail calls are served from a fixed
// sequence of canned page responses, so history pagination can be tested without
// spawning the gws binary.
func stubbedNotifier(t *testing.T, pages []string) *notifier {
	t.Helper()
	n, _ := setupNotifier(t)
	n.depsMap = func() map[credential.CredentialSetID]Deps {
		return map[credential.CredentialSetID]Deps{testCredSet: {}}
	}
	idx := 0
	n.run = func(_ context.Context, _ Deps, _ gws.Command) (json.RawMessage, error) {
		if idx >= len(pages) {
			return nil, errors.New("notifier requested more pages than the stub provides")
		}
		out := pages[idx]
		idx++
		return json.RawMessage(out), nil
	}
	return n
}

type noopEmitter struct {
	mu       sync.Mutex
	messages []notification.Notification
}

func (e *noopEmitter) Emit(_ context.Context, n notification.Notification) error {
	e.mu.Lock()
	e.messages = append(e.messages, n)
	e.mu.Unlock()
	return nil
}
