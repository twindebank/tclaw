package e2etest

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestCrossChannel(t *testing.T) {
	t.Run("both channels get responses", func(t *testing.T) {
		h := NewHarness(t, Config{
			Channels: []ChannelConfig{
				{Name: "desktop"},
				{Name: "phone"},
			},
			CommandFunc: Turn{
				Delay:  50 * time.Millisecond,
				Blocks: []Block{TextBlock("done")},
			}.CommandFunc(),
		})

		h.Channel("desktop").Inject("task A")
		h.Channel("phone").Inject("task B")
		h.Channel("desktop").Close()
		h.Channel("phone").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		// Both channels should have received at least one turn.
		require.GreaterOrEqual(t, h.Channel("desktop").Dones(), 1, "desktop should have at least 1 done")
		require.GreaterOrEqual(t, h.Channel("phone").Dones(), 1, "phone should have at least 1 done")
		require.Len(t, h.TurnLog(), 2, "expected 2 turns total")
	})

	t.Run("turn order is recorded", func(t *testing.T) {
		var mu sync.Mutex
		var order []string

		h := NewHarness(t, Config{
			Channels: []ChannelConfig{
				{Name: "first"},
				{Name: "second"},
			},
			CommandFunc: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
				mu.Lock()
				order = append(order, ExtractPrompt(args))
				mu.Unlock()
				return Respond("ok")(ctx, args, env, dir)
			},
		})

		h.Channel("first").Inject("msg-A")
		h.Channel("second").Inject("msg-B")
		h.Channel("first").Close()
		h.Channel("second").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		mu.Lock()
		require.Len(t, order, 2, "expected 2 turns")
		mu.Unlock()
	})

	// Reproduces production bug: cross-channel message gets stuck in queue.
	// Flow: email processes → sends cross-channel to main → email finishes →
	// main should process the queued message.
	t.Run("cross-channel message unblocks after source turn ends", func(t *testing.T) {
		emailTurnStarted := make(chan struct{})
		var mainTurnRan int32

		h := NewHarness(t, Config{
			Channels: []ChannelConfig{
				{Name: "email"},
				{Name: "main"},
			},
			CommandFunc: Scripted([]Script{
				{
					Match: MatchPrompt("process this email"),
					Respond: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
						close(emailTurnStarted)
						time.Sleep(200 * time.Millisecond)
						return Respond("email processed")(ctx, args, env, dir)
					},
				},
				{
					Match: MatchPrompt("cross-channel from email"),
					Respond: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
						atomic.StoreInt32(&mainTurnRan, 1)
						return Respond("handled cross-channel")(ctx, args, env, dir)
					},
				},
			}, Respond("unknown")),
		})

		h.Channel("email").Inject("process this email")

		go func() {
			<-emailTurnStarted
			time.Sleep(50 * time.Millisecond)
			h.InjectTagged(channel.TaggedMessage{
				ChannelID: h.Channel("main").Info().ID,
				Text:      "cross-channel from email",
				SourceInfo: &channel.MessageSourceInfo{
					Source:      channel.SourceChannel,
					FromChannel: "email",
				},
			})
			h.Channel("email").Close()
			h.Channel("main").Close()
		}()

		require.NoError(t, RunWithTimeout(t, h, 15*time.Second))
		require.Equal(t, int32(1), atomic.LoadInt32(&mainTurnRan),
			"cross-channel message to main should have been processed")
	})

	// Regression test for production bug: cross-channel message blocked by
	// its own arrival resetting the target channel's cooldown timer.
	//
	// Before the fix, the bridge goroutine called MessageReceivedFrom for
	// ALL messages including non-user ones. A cross-channel message targeting
	// main would set lastMessageAt on main at bridge arrival time, then get
	// queued because main was "busy" — blocked behind its own timestamp.
	// The message waited the full 3min cooldown from its own arrival.
	//
	// After the fix, only user/resume messages are tracked at the bridge.
	// Non-user messages get tracked when they start processing (OnTurnStart).
	t.Run("cross-channel message not blocked by its own arrival", func(t *testing.T) {
		emailTurnStarted := make(chan struct{})
		var mainTurnRan int32

		h := NewHarness(t, Config{
			Channels: []ChannelConfig{
				{Name: "email"},
				{Name: "main"},
			},
			CommandFunc: Scripted([]Script{
				{
					Match: MatchPrompt("process this email"),
					Respond: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
						close(emailTurnStarted)
						time.Sleep(200 * time.Millisecond)
						return Respond("email processed")(ctx, args, env, dir)
					},
				},
				{
					Match: MatchPrompt("cross-channel from email"),
					Respond: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
						atomic.StoreInt32(&mainTurnRan, 1)
						return Respond("handled cross-channel")(ctx, args, env, dir)
					},
				},
			}, Respond("unknown")),
		})

		// Main has NO recent user activity — the only message targeting main
		// is the cross-channel one. Before the fix, this message would set
		// lastMessageAt on main at the bridge, making main appear "busy"
		// and blocking itself for 3 minutes.
		h.Channel("email").Inject("process this email")

		go func() {
			<-emailTurnStarted
			time.Sleep(50 * time.Millisecond)
			h.InjectTagged(channel.TaggedMessage{
				ChannelID: h.Channel("main").Info().ID,
				Text:      "cross-channel from email",
				SourceInfo: &channel.MessageSourceInfo{
					Source:      channel.SourceChannel,
					FromChannel: "email",
				},
			})
			h.Channel("email").Close()
			h.Channel("main").Close()
		}()

		// Should complete in seconds, not 3+ minutes.
		require.NoError(t, RunWithTimeout(t, h, 15*time.Second))
		require.Equal(t, int32(1), atomic.LoadInt32(&mainTurnRan),
			"cross-channel message to idle main should not be blocked by its own arrival")
	})
}
