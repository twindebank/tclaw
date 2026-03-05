package channel

import (
	"bufio"
	"context"
	"fmt"
	"os"
)

// Stdio reads from stdin and writes to stdout.
type Stdio struct{}

func NewStdio() *Stdio { return &Stdio{} }

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

func (s *Stdio) Send(_ context.Context, text string) error {
	fmt.Print(text)
	return nil
}
