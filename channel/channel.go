package channel

import "context"

// Channel is the interface every transport must implement.
type Channel interface {
	// Messages returns a channel of incoming user messages.
	Messages(ctx context.Context) <-chan string
	// Send delivers a chunk of response to the user.
	Send(ctx context.Context, text string) error
	// Done signals the end of a response turn.
	Done(ctx context.Context) error
}
