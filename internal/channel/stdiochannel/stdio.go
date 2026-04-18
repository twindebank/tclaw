package stdiochannel

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"tclaw/internal/channel"
)

// Stdio reads from stdin and writes to stdout.
type Stdio struct{}

func NewStdio() *Stdio { return &Stdio{} }

func (s *Stdio) Info() channel.Info {
	return channel.Info{
		ID:   "stdio",
		Type: channel.TypeStdio,
		Name: "stdio",
	}
}

func (s *Stdio) Messages(ctx context.Context) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			if text == "" {
				continue
			}
			select {
			case out <- text:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (s *Stdio) Send(_ context.Context, text string, _ channel.SendOpts) (channel.MessageID, error) {
	fmt.Print(text)
	return "", nil
}

func (s *Stdio) Edit(_ context.Context, _ channel.MessageID, text string) error {
	fmt.Print(text)
	return nil
}

func (s *Stdio) Done(_ context.Context) error {
	return nil
}

func (s *Stdio) SplitStatusMessages() bool {
	return false
}

func (s *Stdio) Markup() channel.Markup {
	return channel.MarkupMarkdown
}

func (s *Stdio) StatusWrap() channel.StatusWrap {
	return channel.StatusWrap{}
}
