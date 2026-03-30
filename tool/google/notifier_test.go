package google

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/credential"
	"tclaw/notification"
)

func emptyDepsMap() map[credential.CredentialSetID]Deps { return nil }

func TestNotifier_NotificationTypes(t *testing.T) {
	t.Run("declares new_email with correct scopes", func(t *testing.T) {
		n := newNotifier(emptyDepsMap)
		types := n.NotificationTypes()

		require.Len(t, types, 1)
		require.Equal(t, TypeNewEmail, types[0].Name)
		require.Contains(t, types[0].Scopes, notification.ScopeCredential)
		require.Contains(t, types[0].Scopes, notification.ScopePersistent)
	})
}

func TestNotifier_Subscribe(t *testing.T) {
	t.Run("builds subscription with correct fields", func(t *testing.T) {
		n := newNotifier(emptyDepsMap)

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
		n := newNotifier(emptyDepsMap)
		_, err := n.Subscribe(context.Background(), notification.SubscribeParams{
			TypeName: "nonexistent",
		}, &noopEmitter{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown notification type")
	})
}

func TestNotifier_Cancel(t *testing.T) {
	t.Run("is idempotent", func(t *testing.T) {
		n := newNotifier(emptyDepsMap)

		result, err := n.Subscribe(context.Background(), notification.SubscribeParams{
			TypeName:    TypeNewEmail,
			ChannelName: "main",
			Scope:       notification.ScopePersistent,
		}, &noopEmitter{})
		require.NoError(t, err)

		result.Cancel()
		result.Cancel()
		n.Cancel(result.Subscription.ID)
	})
}

func TestNotifier_Resubscribe(t *testing.T) {
	t.Run("restarts from persisted config", func(t *testing.T) {
		n := newNotifier(emptyDepsMap)

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

	t.Run("preserves history ID from persisted config", func(t *testing.T) {
		n := newNotifier(emptyDepsMap)

		config := gmailPollConfig{
			CredentialSetID: "google/work",
			Interval:        defaultPollInterval,
			HistoryID:       "12345",
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

		cancel, err := n.Resubscribe(context.Background(), sub, &noopEmitter{})
		require.NoError(t, err)
		require.NotNil(t, cancel)
		cancel()
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
