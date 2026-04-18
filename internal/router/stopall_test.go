package router

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/mcp"
	"tclaw/internal/user"
)

func TestStopAll(t *testing.T) {
	t.Run("sends shutdown notification to lifecycle channels", func(t *testing.T) {
		lifecycleCh := &recordingChannel{id: "tg", name: "telegram"}
		nonLifecycleCh := &recordingChannel{id: "sock", name: "socket"}

		// Registry with telegram marked as lifecycle channel.
		registry := channel.NewRegistry([]channel.RegistryEntry{
			{Info: channel.Info{ID: "tg", Name: "telegram", NotifyLifecycle: true}},
			{Info: channel.Info{ID: "sock", Name: "socket"}},
		})

		cs := NewChannelSet(map[channel.ChannelID]channel.Channel{
			"tg":   lifecycleCh,
			"sock": nonLifecycleCh,
		})

		r := newTestRouter(t)
		r.users["user1"] = &managedUser{
			cfg:        user.Config{ID: "user1"},
			channelSet: cs,
			registry:   registry,
		}

		r.StopAll()

		require.True(t, lifecycleCh.sendCalled.Load(), "lifecycle channel should receive shutdown notification")
		require.Contains(t, lifecycleCh.lastMessage.Load(), "Shutting down")
		require.False(t, nonLifecycleCh.sendCalled.Load(), "non-lifecycle channel should not receive shutdown notification")
	})

	t.Run("skips users with nil channelSet", func(t *testing.T) {
		// Simulates StopAll being called before waitAndStart populates channelSet.
		r := newTestRouter(t)
		r.users["user1"] = &managedUser{
			cfg:      user.Config{ID: "user1"},
			registry: channel.NewRegistry(nil),
			// channelSet intentionally nil
		}

		// Should not panic.
		r.StopAll()
	})

	t.Run("skips users with nil registry", func(t *testing.T) {
		r := newTestRouter(t)
		r.users["user1"] = &managedUser{
			cfg:        user.Config{ID: "user1"},
			channelSet: NewChannelSet(nil),
			// registry intentionally nil
		}

		// Should not panic.
		r.StopAll()
	})
}

// --- helpers ---

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	return &Router{
		users:      make(map[user.ID]*managedUser),
		mcpServers: make(map[user.ID]*mcp.Server),
	}
}

// recordingChannel is a stub that records whether Send was called and what was sent.
type recordingChannel struct {
	id          channel.ChannelID
	name        string
	sendCalled  atomic.Bool
	lastMessage atomic.Value // string
}

func (c *recordingChannel) Info() channel.Info {
	return channel.Info{ID: c.id, Name: c.name}
}
func (c *recordingChannel) Messages(_ context.Context) <-chan string { return nil }
func (c *recordingChannel) Send(_ context.Context, msg string, _ channel.SendOpts) (channel.MessageID, error) {
	c.sendCalled.Store(true)
	c.lastMessage.Store(msg)
	return "", nil
}
func (c *recordingChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error {
	return nil
}
func (c *recordingChannel) Done(_ context.Context) error   { return nil }
func (c *recordingChannel) SplitStatusMessages() bool      { return false }
func (c *recordingChannel) Markup() channel.Markup         { return channel.MarkupMarkdown }
func (c *recordingChannel) StatusWrap() channel.StatusWrap { return channel.StatusWrap{} }
