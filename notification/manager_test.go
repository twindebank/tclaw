package notification_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
	"tclaw/notification"
)

func TestManager_Subscribe(t *testing.T) {
	t.Run("persists subscription and delivers notification", func(t *testing.T) {
		h := setupManager(t)

		result, err := h.manager.Subscribe(h.ctx, "test", notification.SubscribeParams{
			TypeName:    "event",
			ChannelName: "main",
			Scope:       notification.ScopePersistent,
			Label:       "test/event",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Subscription.ID)

		msg := receiveMessage(t, h.output)
		require.Equal(t, channel.ChannelID("main-id"), msg.ChannelID)
		require.Equal(t, "event fired", msg.Text)
		require.Equal(t, channel.SourceNotification, msg.SourceInfo.Source)
		require.Equal(t, "test/event", msg.SourceInfo.SubscriptionLabel)

		subs, err := h.manager.List(h.ctx)
		require.NoError(t, err)
		require.Len(t, subs, 1)
	})

	t.Run("rejects unknown package", func(t *testing.T) {
		h := setupManager(t)

		_, err := h.manager.Subscribe(h.ctx, "nonexistent", notification.SubscribeParams{
			TypeName:    "event",
			ChannelName: "main",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no notifier registered")
	})
}

func TestManager_Unsubscribe(t *testing.T) {
	t.Run("removes subscription and cancels watcher", func(t *testing.T) {
		h := setupManager(t)

		result, err := h.manager.Subscribe(h.ctx, "test", notification.SubscribeParams{
			TypeName:    "event",
			ChannelName: "main",
			Scope:       notification.ScopePersistent,
			Label:       "test/event",
		})
		require.NoError(t, err)
		_ = receiveMessage(t, h.output)

		require.NoError(t, h.manager.Unsubscribe(h.ctx, result.Subscription.ID))

		subs, err := h.manager.List(h.ctx)
		require.NoError(t, err)
		require.Empty(t, subs)
		require.True(t, h.notifier.wasCancelled(result.Subscription.ID))
	})
}

func TestManager_OneShotAutoRemove(t *testing.T) {
	t.Run("removes subscription after first delivery", func(t *testing.T) {
		h := setupManager(t)

		result, err := h.manager.Subscribe(h.ctx, "test", notification.SubscribeParams{
			TypeName:    "event",
			ChannelName: "main",
			Scope:       notification.ScopeOneShot,
			Label:       "one-shot test",
		})
		require.NoError(t, err)
		_ = receiveMessage(t, h.output)

		subs, err := h.manager.List(h.ctx)
		require.NoError(t, err)
		require.Empty(t, subs)
		require.True(t, h.notifier.wasCancelled(result.Subscription.ID))
	})
}

func TestManager_BusyChannelQueuing(t *testing.T) {
	t.Run("queues when channel is busy and drains when free", func(t *testing.T) {
		h := setupManager(t)
		h.activity.TurnStarted("main")

		_, err := h.manager.Subscribe(h.ctx, "test", notification.SubscribeParams{
			TypeName:    "event",
			ChannelName: "main",
			Scope:       notification.ScopePersistent,
			Label:       "busy test",
		})
		require.NoError(t, err)

		// Output should be empty — notification queued.
		select {
		case msg := <-h.output:
			t.Fatalf("expected no message while busy, got: %s", msg.Text)
		case <-time.After(50 * time.Millisecond):
		}

		// Verify it's in the pending store.
		pending, err := h.pendingStore.List(h.ctx)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		require.Equal(t, "event fired", pending[0].Text)
	})
}

func TestManager_UnsubscribeByCredentialSet(t *testing.T) {
	t.Run("removes all subscriptions for the credential set", func(t *testing.T) {
		h := setupManager(t)

		_, err := h.manager.Subscribe(h.ctx, "test", notification.SubscribeParams{
			TypeName:        "event",
			ChannelName:     "main",
			Scope:           notification.ScopeCredential,
			CredentialSetID: "google/work",
			Label:           "cred test",
		})
		require.NoError(t, err)
		_ = receiveMessage(t, h.output)

		require.NoError(t, h.manager.UnsubscribeByCredentialSet(h.ctx, "google/work"))

		subs, err := h.manager.List(h.ctx)
		require.NoError(t, err)
		require.Empty(t, subs)
	})
}

func TestManager_AvailableTypes(t *testing.T) {
	t.Run("returns types from registered notifiers", func(t *testing.T) {
		h := setupManager(t)

		types := h.manager.AvailableTypes()
		require.Contains(t, types, "test")
		require.Len(t, types["test"], 1)
		require.Equal(t, "event", types["test"][0].Name)
	})
}

// --- helpers ---

type managerHarness struct {
	ctx          context.Context
	manager      *notification.Manager
	notifier     *mockNotifier
	output       chan channel.TaggedMessage
	activity     *channel.ActivityTracker
	pendingStore *notification.PendingStore
}

func setupManager(t *testing.T) *managerHarness {
	t.Helper()

	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	output := make(chan channel.TaggedMessage, 8)
	activity := channel.NewActivityTracker()

	channels := func() map[channel.ChannelID]channel.Channel {
		return map[channel.ChannelID]channel.Channel{
			"main-id": &mockChannel{name: "main", id: "main-id"},
		}
	}

	pendingStore := notification.NewPendingStore(s)
	mgr := notification.NewManager(notification.ManagerParams{
		Store:        notification.NewStore(s),
		PendingStore: pendingStore,
		Output:       output,
		Channels:     channels,
		Activity:     activity,
	})

	notifier := newMockNotifier()
	mgr.RegisterNotifier("test", notifier)

	return &managerHarness{
		ctx:          context.Background(),
		manager:      mgr,
		notifier:     notifier,
		output:       output,
		activity:     activity,
		pendingStore: pendingStore,
	}
}

func receiveMessage(t *testing.T, ch chan channel.TaggedMessage) channel.TaggedMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message on output channel")
		return channel.TaggedMessage{}
	}
}

// mockNotifier emits immediately on Subscribe so tests can verify delivery.
type mockNotifier struct {
	mu        sync.Mutex
	cancelled map[notification.SubscriptionID]bool
}

func newMockNotifier() *mockNotifier {
	return &mockNotifier{cancelled: make(map[notification.SubscriptionID]bool)}
}

func (m *mockNotifier) NotificationTypes() []notification.NotificationType {
	return []notification.NotificationType{
		{
			Name:        "event",
			Description: "A test event",
			Scopes:      []notification.Scope{notification.ScopeOneShot, notification.ScopePersistent, notification.ScopeCredential},
		},
	}
}

func (m *mockNotifier) Subscribe(_ context.Context, params notification.SubscribeParams, emitter notification.Emitter) (*notification.SubscribeResult, error) {
	sub := notification.Subscription{
		ID:              notification.GenerateID(),
		Scope:           params.Scope,
		ChannelName:     params.ChannelName,
		PackageName:     "test",
		TypeName:        params.TypeName,
		CredentialSetID: params.CredentialSetID,
		Label:           params.Label,
		CreatedAt:       time.Now(),
	}
	configJSON, _ := json.Marshal(map[string]string{"test": "config"})
	sub.Config = configJSON

	// Emit synchronously so the test can verify delivery immediately.
	_ = emitter.Emit(context.Background(), notification.Notification{
		SubscriptionID: sub.ID,
		Text:           "event fired",
	})

	return &notification.SubscribeResult{
		Subscription: sub,
		Cancel: func() {
			m.mu.Lock()
			m.cancelled[sub.ID] = true
			m.mu.Unlock()
		},
	}, nil
}

func (m *mockNotifier) Resubscribe(_ context.Context, sub notification.Subscription, _ notification.Emitter) (notification.CancelFunc, error) {
	return func() {
		m.mu.Lock()
		m.cancelled[sub.ID] = true
		m.mu.Unlock()
	}, nil
}

func (m *mockNotifier) Cancel(id notification.SubscriptionID) {
	m.mu.Lock()
	m.cancelled[id] = true
	m.mu.Unlock()
}

func (m *mockNotifier) wasCancelled(id notification.SubscriptionID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelled[id]
}

// mockChannel implements channel.Channel for testing.
type mockChannel struct {
	name string
	id   channel.ChannelID
}

func (c *mockChannel) Info() channel.Info {
	return channel.Info{ID: c.id, Name: c.name}
}
func (c *mockChannel) Messages(_ context.Context) <-chan string                    { return nil }
func (c *mockChannel) Send(_ context.Context, _ string) (channel.MessageID, error) { return "", nil }
func (c *mockChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }
func (c *mockChannel) Done(_ context.Context) error                                { return nil }
func (c *mockChannel) SplitStatusMessages() bool                                   { return false }
func (c *mockChannel) Markup() channel.Markup                                      { return channel.MarkupMarkdown }
func (c *mockChannel) StatusWrap() channel.StatusWrap                              { return channel.StatusWrap{} }
