package channel_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"tclaw/internal/channel"
)

func TestMergeFanIns_CombinesTwoSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1 := make(chan channel.TaggedMessage, 1)
	ch2 := make(chan channel.TaggedMessage, 1)

	merged := channel.MergeFanIns(ctx, ch1, ch2)

	ch1 <- channel.TaggedMessage{ChannelID: "a", Text: "hello"}
	ch2 <- channel.TaggedMessage{ChannelID: "b", Text: "world"}

	var got []string
	for i := 0; i < 2; i++ {
		select {
		case msg := <-merged:
			got = append(got, string(msg.ChannelID)+":"+msg.Text)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for merged message")
		}
	}

	sort.Strings(got)
	if got[0] != "a:hello" || got[1] != "b:world" {
		t.Fatalf("unexpected messages: %v", got)
	}
}

func TestMergeFanIns_NilSourcesIgnored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1 := make(chan channel.TaggedMessage, 1)
	merged := channel.MergeFanIns(ctx, nil, ch1, nil)

	ch1 <- channel.TaggedMessage{ChannelID: "a", Text: "hi"}

	select {
	case msg := <-merged:
		if msg.Text != "hi" {
			t.Fatalf("expected 'hi', got %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestMergeFanIns_ClosesWhenAllSourcesDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1 := make(chan channel.TaggedMessage)
	close(ch1)

	merged := channel.MergeFanIns(ctx, ch1)

	select {
	case _, ok := <-merged:
		if ok {
			t.Fatal("expected merged channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for merged channel to close")
	}
}

func TestMergeFanIns_CancelStopsMerge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Unbuffered channel that will never send — only way merged closes is via cancel.
	ch1 := make(chan channel.TaggedMessage)
	merged := channel.MergeFanIns(ctx, ch1)

	cancel()

	select {
	case <-merged:
		// closed, good
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for merged channel to close after cancel")
	}
}
