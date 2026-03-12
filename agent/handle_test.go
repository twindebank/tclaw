package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tclaw/channel"
)

// mockChannel records Send/Edit calls and can be configured to fail.
type mockChannel struct {
	sends     []string          // text of each Send call
	edits     []mockEdit        // (id, text) of each Edit call
	nextID    int               // auto-increment for message IDs
	editError error             // if set, Edit returns this error
	info      channel.Info
}

type mockEdit struct {
	id   channel.MessageID
	text string
}

func (m *mockChannel) Info() channel.Info                              { return m.info }
func (m *mockChannel) Messages(_ context.Context) <-chan string        { return nil }
func (m *mockChannel) Done(_ context.Context) error                    { return nil }
func (m *mockChannel) SplitStatusMessages() bool                       { return true }
func (m *mockChannel) Markup() channel.Markup                          { return channel.MarkupHTML }

func (m *mockChannel) Send(_ context.Context, text string) (channel.MessageID, error) {
	m.nextID++
	id := channel.MessageID(strings.Repeat("m", m.nextID))
	m.sends = append(m.sends, text)
	return id, nil
}

func (m *mockChannel) Edit(_ context.Context, id channel.MessageID, text string) error {
	if m.editError != nil {
		return m.editError
	}
	m.edits = append(m.edits, mockEdit{id: id, text: text})
	return nil
}

func TestWriteSplit_ProactiveSplitStatus(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// First write creates a new message.
	if err := tw.writeSplit(phaseStatus, "start\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ch.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(ch.sends))
	}

	// Write enough to exceed maxMessageLen.
	bigChunk := strings.Repeat("x", maxMessageLen)
	if err := tw.writeSplit(phaseStatus, bigChunk); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have sent a second message (the rotation).
	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after proactive split, got %d", len(ch.sends))
	}
	// The new message should contain only the big chunk, not the old content.
	if ch.sends[1] != bigChunk {
		t.Fatalf("expected new message to contain only the new chunk, got %d chars", len(ch.sends[1]))
	}
}

func TestWriteSplit_ProactiveSplitResponse(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	if err := tw.writeSplit(phaseResponse, "start\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ch.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(ch.sends))
	}

	bigChunk := strings.Repeat("y", maxMessageLen)
	if err := tw.writeSplit(phaseResponse, bigChunk); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after proactive split, got %d", len(ch.sends))
	}
	if ch.sends[1] != bigChunk {
		t.Fatalf("expected new message to contain only the new chunk, got %d chars", len(ch.sends[1]))
	}
}

func TestWriteSplit_EditFailureRecovery_Status(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// First write creates message.
	if err := tw.writeSplit(phaseStatus, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Make Edit fail.
	ch.editError = errors.New("MESSAGE_TOO_LONG")

	// Second write should recover: log error, send new message, not return error.
	if err := tw.writeSplit(phaseStatus, " world"); err != nil {
		t.Fatalf("status edit failure should not propagate, got: %v", err)
	}

	// Should have 2 sends: original + recovery.
	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after recovery, got %d", len(ch.sends))
	}
	// Recovery message should only contain the new text.
	if ch.sends[1] != " world" {
		t.Fatalf("expected recovery message to be ' world', got %q", ch.sends[1])
	}
}

func TestWriteSplit_EditFailureRecovery_Response(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// First write creates message.
	if err := tw.writeSplit(phaseResponse, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Make Edit fail.
	ch.editError = errors.New("MESSAGE_TOO_LONG")

	// Second write should recover with a new Send.
	if err := tw.writeSplit(phaseResponse, " world"); err != nil {
		t.Fatalf("response edit failure should recover, got: %v", err)
	}

	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after recovery, got %d", len(ch.sends))
	}
	if ch.sends[1] != " world" {
		t.Fatalf("expected recovery message to be ' world', got %q", ch.sends[1])
	}
}

func TestWriteSplit_NoSplitBelowThreshold(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// Write many small chunks that stay under the limit.
	for i := 0; i < 100; i++ {
		if err := tw.writeSplit(phaseStatus, "ok\n"); err != nil {
			t.Fatalf("unexpected error on write %d: %v", i, err)
		}
	}

	// Only the first write should Send; all others should Edit.
	if len(ch.sends) != 1 {
		t.Fatalf("expected 1 send for small writes, got %d", len(ch.sends))
	}
	if len(ch.edits) != 99 {
		t.Fatalf("expected 99 edits, got %d", len(ch.edits))
	}
}
