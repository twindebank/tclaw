package stdiochannel

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"tclaw/channel"
)

// Oneshot is a channel that delivers a single pre-loaded message and prints
// all output to stdout. After the first turn completes (Done is called),
// it cancels the provided context to shut down the agent.
//
// Used by `tclaw oneshot` for quick local testing without deploying.
type Oneshot struct {
	message  string
	cancel   context.CancelFunc
	msgCount atomic.Int64

	// telegram emulates Telegram channel behavior (split status messages,
	// HTML markup, expandable blockquotes) so formatting can be tested locally.
	// In telegram mode, every send/edit is printed verbatim with labels.
	// In normal mode, only deltas are printed for clean output.
	telegram bool

	// lastPrinted tracks per-message printed length so edits only output
	// the new content (avoids re-printing the full buffer on each edit).
	lastPrinted map[channel.MessageID]int
}

// NewOneshot creates a channel that delivers message once and exits after the
// first turn. If telegramMode is true, it emulates Telegram's formatting
// behavior (split messages, HTML, expandable blockquotes).
func NewOneshot(message string, cancel context.CancelFunc, telegramMode bool) *Oneshot {
	return &Oneshot{
		message:     message,
		cancel:      cancel,
		telegram:    telegramMode,
		lastPrinted: make(map[channel.MessageID]int),
	}
}

func (o *Oneshot) Info() channel.Info {
	return channel.Info{
		ID:   "oneshot",
		Type: channel.TypeStdio,
		Name: "oneshot",
	}
}

func (o *Oneshot) Messages(ctx context.Context) <-chan string {
	out := make(chan string, 1)
	out <- o.message
	// Don't close — the agent's inner loop cancels the turn if msgs closes.
	// The Done() method cancels the context to shut everything down cleanly.
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out
}

func (o *Oneshot) Send(_ context.Context, text string) (channel.MessageID, error) {
	n := o.msgCount.Add(1)
	id := channel.MessageID(fmt.Sprintf("msg-%d", n))
	if o.telegram {
		fmt.Fprintf(os.Stderr, "[send %s]\n", id)
		fmt.Fprint(os.Stdout, text)
	} else {
		fmt.Fprint(os.Stdout, text)
		o.lastPrinted[id] = len(text)
	}
	return id, nil
}

func (o *Oneshot) Edit(_ context.Context, id channel.MessageID, text string) error {
	if o.telegram {
		// In telegram mode, show each edit verbatim so the user can see
		// the exact content that would be sent to Telegram.
		fmt.Fprintf(os.Stderr, "[edit %s]\n", id)
		fmt.Fprint(os.Stdout, text)
	} else {
		// In normal mode, only print new content since the last send/edit.
		prev := o.lastPrinted[id]
		if len(text) > prev {
			fmt.Fprint(os.Stdout, text[prev:])
		}
		o.lastPrinted[id] = len(text)
	}
	return nil
}

func (o *Oneshot) Done(_ context.Context) error {
	fmt.Fprintln(os.Stdout)
	o.cancel()
	return nil
}

func (o *Oneshot) SplitStatusMessages() bool {
	return o.telegram
}

func (o *Oneshot) Markup() channel.Markup {
	if o.telegram {
		return channel.MarkupHTML
	}
	return channel.MarkupMarkdown
}

func (o *Oneshot) StatusWrap() channel.StatusWrap {
	if o.telegram {
		return channel.StatusWrap{Open: "<blockquote expandable>", Close: "</blockquote>"}
	}
	return channel.StatusWrap{}
}
