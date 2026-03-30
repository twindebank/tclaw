package notificationtools_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/notification"
	"tclaw/tool/notificationtools"
)

func TestNotificationTypes(t *testing.T) {
	t.Run("returns registered notification types", func(t *testing.T) {
		h, _ := setup(t)

		result := callTool(t, h, notificationtools.ToolTypes, map[string]any{})

		var types map[string][]notification.NotificationType
		require.NoError(t, json.Unmarshal(result, &types))
		require.Contains(t, types, "test")
		require.Len(t, types["test"], 1)
		require.Equal(t, "event", types["test"][0].Name)
	})

	t.Run("returns message when no notifiers registered", func(t *testing.T) {
		h, _ := setupEmpty(t)

		result := callTool(t, h, notificationtools.ToolTypes, map[string]any{})

		var msg string
		require.NoError(t, json.Unmarshal(result, &msg))
		require.Contains(t, msg, "No notification types")
	})
}

func TestNotificationSubscribe(t *testing.T) {
	t.Run("creates subscription and returns ID", func(t *testing.T) {
		h, mgr := setup(t)

		result := callTool(t, h, notificationtools.ToolSubscribe, map[string]any{
			"package_name": "test",
			"type":         "event",
			"channel_name": "main",
			"scope":        "persistent",
			"label":        "my test sub",
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.NotEmpty(t, got["subscription_id"])
		require.Equal(t, "my test sub", got["label"])
		require.Equal(t, "persistent", got["scope"])

		// Verify actually persisted.
		subs, err := mgr.List(context.Background())
		require.NoError(t, err)
		require.Len(t, subs, 1)
	})

	t.Run("rejects missing package_name", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, notificationtools.ToolSubscribe, map[string]any{
			"type":         "event",
			"channel_name": "main",
			"scope":        "persistent",
		})
		require.Contains(t, err.Error(), "package_name is required")
	})

	t.Run("rejects invalid scope", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, notificationtools.ToolSubscribe, map[string]any{
			"package_name": "test",
			"type":         "event",
			"channel_name": "main",
			"scope":        "bogus",
		})
		require.Contains(t, err.Error(), "invalid scope")
	})

	t.Run("rejects credential scope without credential_set_id", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, notificationtools.ToolSubscribe, map[string]any{
			"package_name": "test",
			"type":         "event",
			"channel_name": "main",
			"scope":        "credential",
		})
		require.Contains(t, err.Error(), "credential_set_id is required")
	})

	t.Run("rejects unknown package", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, notificationtools.ToolSubscribe, map[string]any{
			"package_name": "nonexistent",
			"type":         "event",
			"channel_name": "main",
			"scope":        "persistent",
		})
		require.Contains(t, err.Error(), "no notifier registered")
	})
}

func TestNotificationUnsubscribe(t *testing.T) {
	t.Run("removes subscription", func(t *testing.T) {
		h, mgr := setup(t)

		// Subscribe first.
		subResult := callTool(t, h, notificationtools.ToolSubscribe, map[string]any{
			"package_name": "test",
			"type":         "event",
			"channel_name": "main",
			"scope":        "persistent",
		})
		var sub map[string]any
		require.NoError(t, json.Unmarshal(subResult, &sub))
		subID := sub["subscription_id"].(string)

		// Unsubscribe.
		result := callTool(t, h, notificationtools.ToolUnsubscribe, map[string]any{
			"subscription_id": subID,
		})
		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Contains(t, got["message"], subID)

		// Verify removed.
		subs, err := mgr.List(context.Background())
		require.NoError(t, err)
		require.Empty(t, subs)
	})

	t.Run("rejects missing subscription_id", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, notificationtools.ToolUnsubscribe, map[string]any{})
		require.Contains(t, err.Error(), "subscription_id is required")
	})
}

func TestNotificationList(t *testing.T) {
	t.Run("returns subscriptions", func(t *testing.T) {
		h, _ := setup(t)

		// Subscribe.
		callTool(t, h, notificationtools.ToolSubscribe, map[string]any{
			"package_name": "test",
			"type":         "event",
			"channel_name": "main",
			"scope":        "persistent",
			"label":        "my sub",
		})

		result := callTool(t, h, notificationtools.ToolList, map[string]any{})

		var entries []map[string]any
		require.NoError(t, json.Unmarshal(result, &entries))
		require.Len(t, entries, 1)
		require.Equal(t, "test", entries[0]["package_name"])
		require.Equal(t, "event", entries[0]["type_name"])
		require.Equal(t, "my sub", entries[0]["label"])
	})

	t.Run("returns message when empty", func(t *testing.T) {
		h, _ := setup(t)

		result := callTool(t, h, notificationtools.ToolList, map[string]any{})

		var msg string
		require.NoError(t, json.Unmarshal(result, &msg))
		require.Contains(t, msg, "No active")
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *notification.Manager) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	output := make(chan channel.TaggedMessage, 8)
	channels := func() map[channel.ChannelID]channel.Channel {
		return map[channel.ChannelID]channel.Channel{
			"main-id": &mockChannel{name: "main", id: "main-id"},
		}
	}

	mgr := notification.NewManager(notification.ManagerParams{
		Store:    notification.NewStore(s),
		Output:   output,
		Channels: channels,
	})
	mgr.RegisterNotifier("test", &mockNotifier{})

	handler := mcp.NewHandler()
	notificationtools.RegisterTools(handler, notificationtools.Deps{
		Manager: mgr,
	})

	return handler, mgr
}

func setupEmpty(t *testing.T) (*mcp.Handler, *notification.Manager) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	output := make(chan channel.TaggedMessage, 8)
	mgr := notification.NewManager(notification.ManagerParams{
		Store:    notification.NewStore(s),
		Output:   output,
		Channels: func() map[channel.ChannelID]channel.Channel { return nil },
	})

	handler := mcp.NewHandler()
	notificationtools.RegisterTools(handler, notificationtools.Deps{Manager: mgr})

	return handler, mgr
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
}

// mockNotifier is a minimal Notifier that creates subscriptions without
// actually watching anything.
type mockNotifier struct {
	mu        sync.Mutex
	cancelled map[notification.SubscriptionID]bool
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

func (m *mockNotifier) Subscribe(_ context.Context, params notification.SubscribeParams, _ notification.Emitter) (*notification.SubscribeResult, error) {
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

	return &notification.SubscribeResult{
		Subscription: sub,
		Cancel:       func() {},
	}, nil
}

func (m *mockNotifier) Resubscribe(_ context.Context, _ notification.Subscription, _ notification.Emitter) (notification.CancelFunc, error) {
	return func() {}, nil
}

func (m *mockNotifier) Cancel(id notification.SubscriptionID) {
	if m.cancelled == nil {
		m.cancelled = make(map[notification.SubscriptionID]bool)
	}
	m.mu.Lock()
	m.cancelled[id] = true
	m.mu.Unlock()
}

// mockChannel implements channel.Channel for channel resolution.
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
