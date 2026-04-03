package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestCheckChannelChanged(t *testing.T) {
	t.Run("returns false when changeCh is nil", func(t *testing.T) {
		require.False(t, checkChannelChanged(nil, Options{}, "", nil))
	})

	t.Run("returns false when changeCh is not closed", func(t *testing.T) {
		ch := make(chan struct{})
		require.False(t, checkChannelChanged(ch, Options{}, "", nil))
	})

	t.Run("returns true when changeCh is closed", func(t *testing.T) {
		changeCh := make(chan struct{})
		close(changeCh)

		require.True(t, checkChannelChanged(changeCh, Options{}, "", nil))
	})

	t.Run("sends restart notice to channel", func(t *testing.T) {
		changeCh := make(chan struct{})
		close(changeCh)
		mock := &mockChangeChannel{}

		// Without outbox, opts.send falls through to direct channel call.
		chID := channel.ChannelID("test")
		opts := Options{
			Channels: map[channel.ChannelID]channel.Channel{chID: mock},
		}

		result := checkChannelChanged(changeCh, opts, chID, mock)

		require.True(t, result)
		require.Len(t, mock.sends, 1)
		require.Contains(t, mock.sends[0], "Restarting to apply channel changes")
		require.True(t, mock.doneCalled)
	})

	t.Run("send receives a non-cancelled context", func(t *testing.T) {
		// The key property: checkChannelChanged creates its own context
		// (detached from any parent) so the restart notice works even after
		// a force-kill cancels the agent's context.
		changeCh := make(chan struct{})
		close(changeCh)

		mock := &mockChangeChannel{captureCtx: true}
		chID := channel.ChannelID("test")
		opts := Options{
			Channels: map[channel.ChannelID]channel.Channel{chID: mock},
		}

		result := checkChannelChanged(changeCh, opts, chID, mock)

		require.True(t, result)
		require.Len(t, mock.sends, 1)
		require.NoError(t, mock.sendCtxErr, "Send should receive a non-cancelled context")
	})
}

// --- helpers ---

type mockChangeChannel struct {
	mu         sync.Mutex
	sends      []string
	doneCalled bool
	captureCtx bool
	sendCtxErr error // ctx.Err() captured at Send call time
	info       channel.Info
}

func (m *mockChangeChannel) Info() channel.Info                       { return m.info }
func (m *mockChangeChannel) Messages(_ context.Context) <-chan string { return nil }
func (m *mockChangeChannel) SplitStatusMessages() bool                { return false }
func (m *mockChangeChannel) Markup() channel.Markup                   { return channel.MarkupMarkdown }
func (m *mockChangeChannel) StatusWrap() channel.StatusWrap           { return channel.StatusWrap{} }

func (m *mockChangeChannel) Send(ctx context.Context, text string) (channel.MessageID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, text)
	if m.captureCtx {
		m.sendCtxErr = ctx.Err()
	}
	return "", nil
}

func (m *mockChangeChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }

func (m *mockChangeChannel) Done(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.doneCalled = true
	return nil
}
