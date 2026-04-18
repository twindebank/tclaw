// Package e2etest provides an integration test harness for the agent loop.
// Tests exercise the full pipeline — queue, outbox, turnWriter, streamResponse —
// with only the CLI subprocess replaced by an in-process pipe.
package e2etest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tclaw/internal/channel"
)

// SendRecord captures a single Send() call to the channel.
type SendRecord struct {
	Text   string
	ID     channel.MessageID
	At     time.Time
	Notify bool
}

// EditRecord captures a single Edit() call to the channel.
type EditRecord struct {
	Text string
	ID   channel.MessageID
	At   time.Time
}

// TestChannel implements channel.Channel with injectable input and observable output.
// All methods are safe for concurrent use.
type TestChannel struct {
	info  channel.Info
	split bool

	in     chan string
	closed bool

	mu         sync.Mutex
	sends      []SendRecord
	edits      []EditRecord
	dones      int
	nextID     int
	sendNotify chan struct{}
	doneNotify chan struct{}

	// onDone is called when Done() is received (for auto-shutdown).
	onDone func()
}

// NewTestChannel creates a TestChannel with the given config.
func NewTestChannel(cfg ChannelConfig) *TestChannel {
	name := cfg.Name
	if name == "" {
		name = "main"
	}
	chType := cfg.Type
	if chType == "" {
		chType = channel.TypeSocket
	}
	return &TestChannel{
		info: channel.Info{
			ID:          channel.ChannelID(name + "-id"),
			Type:        chType,
			Name:        name,
			Description: cfg.Description,
			Purpose:     cfg.Purpose,
		},
		split:      cfg.Split,
		in:         make(chan string, 100),
		sendNotify: make(chan struct{}, 1),
		doneNotify: make(chan struct{}, 1),
	}
}

// Inject pushes a user message into the channel.
func (c *TestChannel) Inject(text string) {
	c.in <- text
}

// Close signals no more user messages will be sent. The Messages() output
// channel stays open until its context is cancelled — this avoids a race
// where closing input cancels an in-flight turn.
func (c *TestChannel) Close() {
	if !c.closed {
		c.closed = true
		close(c.in)
	}
}

// Sends returns a thread-safe copy of all Send records.
func (c *TestChannel) Sends() []SendRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SendRecord, len(c.sends))
	copy(out, c.sends)
	return out
}

// Edits returns a thread-safe copy of all Edit records.
func (c *TestChannel) Edits() []EditRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]EditRecord, len(c.edits))
	copy(out, c.edits)
	return out
}

// Dones returns the number of Done() calls.
func (c *TestChannel) Dones() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dones
}

// LastSend returns the text of the most recent Send, or empty string.
func (c *TestChannel) LastSend() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sends) == 0 {
		return ""
	}
	return c.sends[len(c.sends)-1].Text
}

// ResponseText extracts the final visible text from the channel — what the
// user would actually see. In non-split mode the turnWriter accumulates
// everything (status + response) in one message via edits, so the last edit
// is the complete content. In split mode, multiple sends contain separate
// status and response messages.
func (c *TestChannel) ResponseText() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If there are edits, the last edit is the most complete view of the
	// message. This covers non-split mode where status + response accumulate.
	if len(c.edits) > 0 {
		return c.edits[len(c.edits)-1].Text
	}

	// Split mode or no edits: concatenate non-status sends.
	var parts []string
	for _, s := range c.sends {
		if isStatusPrefix(s.Text) {
			continue
		}
		parts = append(parts, s.Text)
	}
	return strings.Join(parts, "")
}

func isStatusPrefix(text string) bool {
	return strings.HasPrefix(text, "🤔") ||
		strings.HasPrefix(text, "🔄") ||
		strings.HasPrefix(text, "⏳") ||
		strings.HasPrefix(text, "⛔")
}

// WaitForSends blocks until at least n sends have been recorded.
func (c *TestChannel) WaitForSends(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		count := len(c.sends)
		c.mu.Unlock()
		if count >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d sends (got %d)", n, count)
		case <-c.sendNotify:
		}
	}
}

// WaitForDone blocks until Done() has been called at least once.
func (c *TestChannel) WaitForDone(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		c.mu.Lock()
		count := c.dones
		c.mu.Unlock()
		if count > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for Done()")
		case <-c.doneNotify:
		}
	}
}

// --- channel.Channel interface ---

func (c *TestChannel) Info() channel.Info { return c.info }

func (c *TestChannel) Messages(ctx context.Context) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-c.in:
				if !ok {
					// Input exhausted. Block until ctx is cancelled rather than
					// closing the output — closing output signals FanIn that this
					// channel is done, which causes the agent to cancel in-flight
					// turns. In tests we want turns to complete before shutdown.
					<-ctx.Done()
					return
				}
				select {
				case out <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (c *TestChannel) Send(_ context.Context, text string, opts channel.SendOpts) (channel.MessageID, error) {
	c.mu.Lock()
	c.nextID++
	id := channel.MessageID(fmt.Sprintf("msg-%d", c.nextID))
	c.sends = append(c.sends, SendRecord{Text: text, ID: id, At: time.Now(), Notify: opts.Notify})
	c.mu.Unlock()

	// Signal waiters non-blocking.
	select {
	case c.sendNotify <- struct{}{}:
	default:
	}

	return id, nil
}

func (c *TestChannel) Edit(_ context.Context, id channel.MessageID, text string) error {
	c.mu.Lock()
	c.edits = append(c.edits, EditRecord{ID: id, Text: text, At: time.Now()})
	c.mu.Unlock()
	return nil
}

func (c *TestChannel) Done(_ context.Context) error {
	c.mu.Lock()
	c.dones++
	c.mu.Unlock()

	select {
	case c.doneNotify <- struct{}{}:
	default:
	}

	if c.onDone != nil {
		c.onDone()
	}

	return nil
}

func (c *TestChannel) SplitStatusMessages() bool      { return c.split }
func (c *TestChannel) Markup() channel.Markup         { return channel.MarkupMarkdown }
func (c *TestChannel) StatusWrap() channel.StatusWrap { return channel.StatusWrap{} }
