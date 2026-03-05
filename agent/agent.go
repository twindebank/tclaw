package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	claude "github.com/character-ai/claude-agent-sdk-go"

	"tclaw/channel"
)

// Agent wraps the Claude Code CLI binary and connects it to a channel.
type Agent struct {
	opts    claude.Options
	channel channel.Channel
}

func New(opts claude.Options, ch channel.Channel) *Agent {
	return &Agent{opts: opts, channel: ch}
}

// Run reads messages from the channel and responds until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	// Call Messages once outside the select — calling it inside would spawn
	// a new goroutine on every iteration.
	msgs := a.channel.Messages(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			if err := a.handle(ctx, msg); err != nil {
				slog.Error("handle failed", "err", err)
			}
			a.channel.Send(ctx, "\n> ") //nolint:errcheck
		}
	}
}

func (a *Agent) handle(ctx context.Context, prompt string) error {
	slog.Info("handling message", "prompt", prompt)

	ag := claude.NewAgent(claude.AgentConfig{
		Options:  a.opts,
		MaxTurns: 20,
	})
	defer ag.Close()

	events, err := ag.Run(ctx, prompt)
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	// Buffer the full response then send it once so channels that care about
	// message boundaries (e.g. Telegram) get a single coherent message.
	var buf strings.Builder
	for event := range events {
		slog.Debug("agent event", "type", event.Type, "content", event.Content, "err", event.Error)
		switch event.Type {
		case claude.AgentEventContentDelta:
			buf.WriteString(event.Content)
		case claude.AgentEventError:
			if event.Error != nil {
				return event.Error
			}
		}
	}

	slog.Info("response ready", "len", buf.Len())
	if text := buf.String(); text != "" {
		return a.channel.Send(ctx, text)
	}
	return nil
}
