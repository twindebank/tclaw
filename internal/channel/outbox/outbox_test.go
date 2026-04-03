package outbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/outbox"
	"tclaw/internal/libraries/store"
)

func TestOutbox(t *testing.T) {
	t.Run("send edit done ordering", func(t *testing.T) {
		rec, ob := setup(t)

		id, err := ob.Send(context.Background(), "ch1", "hello")
		require.NoError(t, err)
		require.NotEmpty(t, id)

		err = ob.Edit(context.Background(), "ch1", id, "hello world")
		require.NoError(t, err)

		err = ob.Done(context.Background(), "ch1")
		require.NoError(t, err)

		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		require.Len(t, calls, 3)
		require.Equal(t, "send", calls[0].Method)
		require.Equal(t, "hello", calls[0].Text)
		require.Equal(t, "edit", calls[1].Method)
		require.Equal(t, "hello world", calls[1].Text)
		require.Equal(t, "done", calls[2].Method)
	})

	t.Run("proxy id resolution", func(t *testing.T) {
		rec, ob := setup(t)

		// The recording channel returns a known real ID from Send.
		rec.SetSendFunc(func(_ context.Context, text string) (channel.MessageID, error) {
			return "real-42", nil
		})

		proxyID, err := ob.Send(context.Background(), "ch1", "msg")
		require.NoError(t, err)

		err = ob.Edit(context.Background(), "ch1", proxyID, "updated")
		require.NoError(t, err)

		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		require.Len(t, calls, 2)
		// The Edit should have received the real ID, not the proxy.
		require.Equal(t, channel.MessageID("real-42"), calls[1].ID)
	})

	t.Run("edit coalescing", func(t *testing.T) {
		// Make Send slow enough that edits pile up in the queue before
		// the delivery goroutine can drain them.
		sendDone := make(chan struct{})
		rec, ob := setup(t)
		rec.SetSendFunc(func(_ context.Context, text string) (channel.MessageID, error) {
			// Block until all edits are enqueued.
			<-sendDone
			return "real-1", nil
		})

		proxyID, err := ob.Send(context.Background(), "ch1", "initial")
		require.NoError(t, err)

		for i := range 50 {
			err = ob.Edit(context.Background(), "ch1", proxyID, fmt.Sprintf("edit-%d", i))
			require.NoError(t, err)
		}

		// Release the Send so delivery starts processing the queued edits.
		close(sendDone)

		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		// Should have Send + 1 Edit (the last one), not Send + 50 Edits.
		require.Len(t, calls, 2, "expected coalescing to reduce edits, got %d calls", len(calls))
		require.Equal(t, "send", calls[0].Method)
		require.Equal(t, "edit", calls[1].Method)
		require.Equal(t, "edit-49", calls[1].Text)
	})

	t.Run("retry on transient failure", func(t *testing.T) {
		rec, ob := setup(t)

		var mu sync.Mutex
		failures := 2
		rec.SetSendFunc(func(_ context.Context, text string) (channel.MessageID, error) {
			mu.Lock()
			defer mu.Unlock()
			if failures > 0 {
				failures--
				return "", fmt.Errorf("connection refused")
			}
			return "ok", nil
		})

		_, err := ob.Send(context.Background(), "ch1", "will retry")
		require.NoError(t, err)

		// Flush waits for delivery to complete (including retries).
		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		// 2 failures + 1 success = 3 Send attempts recorded.
		require.Equal(t, 3, len(calls), "expected 3 attempts (2 failures + 1 success)")
		require.Equal(t, "will retry", calls[2].Text)
	})

	t.Run("permanent error discards operation", func(t *testing.T) {
		rec, ob := setup(t)

		rec.SetSendFunc(func(_ context.Context, text string) (channel.MessageID, error) {
			if text == "blocked" {
				return "", fmt.Errorf("Forbidden: bot was blocked by the user")
			}
			return "ok", nil
		})

		_, err := ob.Send(context.Background(), "ch1", "blocked")
		require.NoError(t, err)
		_, err = ob.Send(context.Background(), "ch1", "next message")
		require.NoError(t, err)

		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		// "blocked" was attempted once and discarded; "next message" should still deliver.
		require.True(t, len(calls) >= 2, "expected at least 2 calls, got %d", len(calls))

		var texts []string
		for _, c := range calls {
			texts = append(texts, c.Text)
		}
		require.Contains(t, texts, "next message", "subsequent message should still be delivered")
	})

	t.Run("done waits for preceding ops", func(t *testing.T) {
		rec, ob := setup(t)

		_, err := ob.Send(context.Background(), "ch1", "msg")
		require.NoError(t, err)
		err = ob.Done(context.Background(), "ch1")
		require.NoError(t, err)

		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		require.Len(t, calls, 2)
		require.Equal(t, "send", calls[0].Method)
		require.Equal(t, "done", calls[1].Method)
	})

	t.Run("persistence round trip", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)

		rec := newRecordingChannel("ch1")

		// Phase 1: enqueue but don't deliver (cancel immediately).
		ctx1, cancel1 := context.WithCancel(context.Background())
		ob1 := outbox.New(outbox.Params{
			Store:    s,
			Channels: channelMap(rec),
		})
		ob1.Start(ctx1)

		_, err = ob1.Send(context.Background(), "ch1", "persisted msg")
		require.NoError(t, err)

		// Give a moment for persistence then shutdown.
		time.Sleep(50 * time.Millisecond)
		cancel1()
		ob1.Stop()

		// Phase 2: new Outbox from same store should replay.
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()
		ob2 := outbox.New(outbox.Params{
			Store:    s,
			Channels: channelMap(rec),
		})
		ob2.Start(ctx2)

		require.NoError(t, ob2.Flush(context.Background()))

		calls := rec.Calls()
		// The persisted Send should have been replayed.
		var found bool
		for _, c := range calls {
			if c.Text == "persisted msg" {
				found = true
				break
			}
		}
		require.True(t, found, "persisted message should have been replayed after restart")
	})

	t.Run("concurrent writers", func(t *testing.T) {
		rec, ob := setup(t)

		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				_, sendErr := ob.Send(context.Background(), "ch1", fmt.Sprintf("msg-%d", n))
				if sendErr != nil {
					t.Errorf("send %d failed: %v", n, sendErr)
				}
			}(i)
		}
		wg.Wait()

		require.NoError(t, ob.Flush(context.Background()))

		calls := rec.Calls()
		require.Len(t, calls, 20, "all concurrent sends should be delivered")
	})

	t.Run("context cancellation persists and shuts down", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)

		// Use a channel that blocks forever so ops stay queued.
		blocking := newBlockingChannel("ch1")

		ctx, cancel := context.WithCancel(context.Background())
		ob := outbox.New(outbox.Params{
			Store:    s,
			Channels: channelMap(blocking),
		})
		ob.Start(ctx)

		_, err = ob.Send(context.Background(), "ch1", "will be persisted")
		require.NoError(t, err)

		// Cancel and wait for shutdown.
		cancel()
		ob.Stop()

		// Verify the op was persisted.
		raw, err := s.Get(context.Background(), "outbox/ch1")
		require.NoError(t, err)
		require.NotEmpty(t, raw, "queue state should be persisted on shutdown")

		var state struct {
			Ops []json.RawMessage `json:"ops"`
		}
		require.NoError(t, json.Unmarshal(raw, &state))
		require.NotEmpty(t, state.Ops, "persisted state should contain the pending op")
	})

	t.Run("flush blocks until drained", func(t *testing.T) {
		rec, ob := setup(t)

		// Add a small delay to Send so flush has something to wait for.
		rec.SetSendFunc(func(_ context.Context, text string) (channel.MessageID, error) {
			time.Sleep(10 * time.Millisecond)
			return "ok", nil
		})

		for i := range 5 {
			_, err := ob.Send(context.Background(), "ch1", fmt.Sprintf("msg-%d", i))
			require.NoError(t, err)
		}

		require.NoError(t, ob.Flush(context.Background()))

		// After Flush returns, all messages should be delivered.
		calls := rec.Calls()
		require.Len(t, calls, 5)
	})

	t.Run("enqueue error propagation", func(t *testing.T) {
		failing := &failingStore{err: fmt.Errorf("disk full")}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		rec := newRecordingChannel("ch1")
		ob := outbox.New(outbox.Params{
			Store:    failing,
			Channels: channelMap(rec),
		})
		ob.Start(ctx)

		// Send persists on enqueue, so it should fail.
		_, err := ob.Send(context.Background(), "ch1", "should fail")
		require.Error(t, err)
		require.Contains(t, err.Error(), "disk full")
	})
}

// --- helpers ---

func setup(t *testing.T) (*recordingChannel, *outbox.Outbox) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	rec := newRecordingChannel("ch1")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		// Give delivery goroutines a moment to exit.
		time.Sleep(20 * time.Millisecond)
	})

	ob := outbox.New(outbox.Params{
		Store:    s,
		Channels: channelMap(rec),
	})
	ob.Start(ctx)

	return rec, ob
}

func channelMap(channels ...channel.Channel) func() map[channel.ChannelID]channel.Channel {
	m := make(map[channel.ChannelID]channel.Channel)
	for _, ch := range channels {
		m[ch.Info().ID] = ch
	}
	return func() map[channel.ChannelID]channel.Channel { return m }
}

// recordedCall captures a single Send/Edit/Done invocation.
type recordedCall struct {
	Method string
	Text   string
	ID     channel.MessageID
}

// recordingChannel implements channel.Channel and records all Send/Edit/Done calls.
type recordingChannel struct {
	name string

	mu       sync.Mutex
	calls    []recordedCall
	sendFunc func(context.Context, string) (channel.MessageID, error)
	editFunc func(context.Context, channel.MessageID, string) error
	counter  int
}

func newRecordingChannel(name string) *recordingChannel {
	return &recordingChannel{
		name: name,
	}
}

func (r *recordingChannel) SetSendFunc(f func(context.Context, string) (channel.MessageID, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sendFunc = f
}

func (r *recordingChannel) Calls() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recordingChannel) Info() channel.Info {
	return channel.Info{ID: channel.ChannelID(r.name), Name: r.name, Type: channel.TypeSocket}
}

func (r *recordingChannel) Messages(context.Context) <-chan string { return make(chan string) }

func (r *recordingChannel) Send(ctx context.Context, text string) (channel.MessageID, error) {
	r.mu.Lock()
	sf := r.sendFunc
	r.counter++
	msgID := channel.MessageID(fmt.Sprintf("real-%d", r.counter))
	r.mu.Unlock()

	if sf != nil {
		id, err := sf(ctx, text)
		r.mu.Lock()
		r.calls = append(r.calls, recordedCall{Method: "send", Text: text, ID: id})
		r.mu.Unlock()
		return id, err
	}

	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{Method: "send", Text: text, ID: msgID})
	r.mu.Unlock()
	return msgID, nil
}

func (r *recordingChannel) Edit(ctx context.Context, id channel.MessageID, text string) error {
	r.mu.Lock()
	ef := r.editFunc
	r.mu.Unlock()

	if ef != nil {
		err := ef(ctx, id, text)
		r.mu.Lock()
		r.calls = append(r.calls, recordedCall{Method: "edit", Text: text, ID: id})
		r.mu.Unlock()
		return err
	}

	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{Method: "edit", Text: text, ID: id})
	r.mu.Unlock()
	return nil
}

func (r *recordingChannel) Done(context.Context) error {
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{Method: "done"})
	r.mu.Unlock()
	return nil
}

func (r *recordingChannel) SplitStatusMessages() bool      { return false }
func (r *recordingChannel) Markup() channel.Markup         { return channel.MarkupMarkdown }
func (r *recordingChannel) StatusWrap() channel.StatusWrap { return channel.StatusWrap{} }

// blockingChannel blocks forever on Send, used to test shutdown persistence.
type blockingChannel struct {
	name string
}

func newBlockingChannel(name string) *blockingChannel {
	return &blockingChannel{name: name}
}

func (b *blockingChannel) Info() channel.Info {
	return channel.Info{ID: channel.ChannelID(b.name), Name: b.name, Type: channel.TypeSocket}
}

func (b *blockingChannel) Messages(context.Context) <-chan string { return make(chan string) }
func (b *blockingChannel) Send(ctx context.Context, _ string) (channel.MessageID, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (b *blockingChannel) Edit(ctx context.Context, _ channel.MessageID, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (b *blockingChannel) Done(context.Context) error     { return nil }
func (b *blockingChannel) SplitStatusMessages() bool      { return false }
func (b *blockingChannel) Markup() channel.Markup         { return channel.MarkupMarkdown }
func (b *blockingChannel) StatusWrap() channel.StatusWrap { return channel.StatusWrap{} }

// failingStore always returns an error on Set.
type failingStore struct {
	err error
}

func (f *failingStore) Get(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (f *failingStore) Set(_ context.Context, _ string, _ []byte) error { return f.err }
func (f *failingStore) Delete(_ context.Context, _ string) error        { return nil }
