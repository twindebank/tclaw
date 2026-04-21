package google

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/credential"
	"tclaw/internal/libraries/store"
	"tclaw/internal/notification"
)

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
	t.Run("is idempotent and cleans up cursor and seen set", func(t *testing.T) {
		n, s := setupNotifier(t)
		ctx := context.Background()

		result, err := n.Subscribe(ctx, notification.SubscribeParams{
			TypeName:    TypeNewEmail,
			ChannelName: "main",
			Scope:       notification.ScopePersistent,
		}, &noopEmitter{})
		require.NoError(t, err)

		// Simulate a persisted cursor and seen set.
		require.NoError(t, s.Set(ctx, cursorKey(result.Subscription.ID), []byte("99999")))
		n.saveSeen(ctx, result.Subscription.ID, []string{"msg1", "msg2"})

		result.Cancel()
		result.Cancel()
		n.Cancel(result.Subscription.ID)

		// Cursor should be cleaned up.
		data, err := s.Get(ctx, cursorKey(result.Subscription.ID))
		require.NoError(t, err)
		require.Empty(t, data)

		// Seen set should be cleaned up.
		require.Empty(t, n.loadSeen(ctx, result.Subscription.ID))
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
		id := notification.GenerateID()

		n.saveSeen(ctx, id, []string{"msg1", "msg2", "msg3"})
		require.Equal(t, []string{"msg1", "msg2", "msg3"}, n.loadSeen(ctx, id))
	})

	t.Run("load returns nil for missing key", func(t *testing.T) {
		n, _ := setupNotifier(t)
		require.Nil(t, n.loadSeen(context.Background(), "notif_nonexistent"))
	})

	t.Run("load returns nil for corrupt data", func(t *testing.T) {
		n, s := setupNotifier(t)
		ctx := context.Background()
		id := notification.GenerateID()

		require.NoError(t, s.Set(ctx, seenKey(id), []byte("not valid json")))
		require.Nil(t, n.loadSeen(ctx, id))
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

func TestFormatNewEmailNotification(t *testing.T) {
	t.Run("single email includes from, subject, and snippet", func(t *testing.T) {
		text := formatNewEmailNotification([]gmailSummary{
			{From: "alice@example.com", Subject: "Meeting tomorrow", Snippet: "Hi, can we meet at 3pm?"},
		})
		require.Contains(t, text, "alice@example.com")
		require.Contains(t, text, "Meeting tomorrow")
		require.Contains(t, text, "Hi, can we meet at 3pm?")
	})

	t.Run("multiple emails shows count and all senders", func(t *testing.T) {
		text := formatNewEmailNotification([]gmailSummary{
			{From: "alice@example.com", Subject: "Meeting"},
			{From: "bob@example.com", Subject: "Invoice"},
			{From: "carol@example.com", Subject: "Hello"},
		})
		require.Contains(t, text, "3 new emails")
		require.Contains(t, text, "alice@example.com")
		require.Contains(t, text, "bob@example.com")
		require.Contains(t, text, "carol@example.com")
	})
}

// --- helpers ---

func setupNotifier(t *testing.T) (*notifier, store.Store) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return newNotifier(emptyDepsMap, s), s
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
