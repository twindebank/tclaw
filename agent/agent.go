package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tclaw/channel"
	"tclaw/secret"
	"tclaw/store"
)

const sessionKeyPrefix = "session:"
const stopKeyword = "stop"

const defaultMaxTurns = 10
const agentIdleTimeout = 10 * time.Minute

// ErrIdleTimeout is returned by RunWithMessages when the agent shuts down
// due to no messages arriving within the idle timeout.
var ErrIdleTimeout = errors.New("agent idle timeout")

// Options configures the agent. All fields are immutable after creation.
type Options struct {
	PermissionMode PermissionMode
	Model          Model

	// MaxTurns limits agentic turns per invocation. Defaults to defaultMaxTurns.
	MaxTurns int

	// Debug logs raw CLI event JSON for troubleshooting.
	Debug bool

	// APIKey is set as ANTHROPIC_API_KEY when spawning the claude subprocess.
	// If empty, the subprocess inherits the parent's environment.
	APIKey string

	// HomeDir is set as HOME for the claude subprocess, isolating all
	// CLI state (~/.claude/) per user. If empty, inherits the parent's HOME.
	HomeDir string

	Channels map[channel.ChannelID]channel.Channel
	Store    store.Store
	Secrets  secret.Store

	// AllowedTools are auto-approved without prompting (e.g. ToolRead, ToolBash.Scoped("git *")).
	AllowedTools []Tool

	// DisallowedTools are removed from the model's context entirely.
	DisallowedTools []Tool

	// SystemPrompt is appended to the default Claude system prompt via
	// --append-system-prompt. Contains agent identity, channel context,
	// and memory instructions.
	SystemPrompt string
}

func sessionKey(chID channel.ChannelID) string {
	return sessionKeyPrefix + string(chID)
}

func loadSession(ctx context.Context, s store.Store, chID channel.ChannelID) (string, error) {
	data, err := s.Get(ctx, sessionKey(chID))
	if err != nil {
		return "", fmt.Errorf("load session: %w", err)
	}
	if len(data) > 0 {
		slog.Info("resumed session", "channel", chID, "session_id", string(data))
		return string(data), nil
	}
	return "", nil
}

func saveSession(ctx context.Context, s store.Store, chID channel.ChannelID, id string) error {
	if err := s.Set(ctx, sessionKey(chID), []byte(id)); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

type handleResult struct {
	sessionID string
	err       error
}

// Run reads messages from all channels and responds until ctx is cancelled.
// Each channel gets its own Claude session for full isolation.
// Sending "stop" interrupts the active turn. Other messages queue behind it.
func Run(ctx context.Context, opts Options) error {
	return RunWithMessages(ctx, opts, channel.FanIn(ctx, opts.Channels))
}

// RunWithMessages is like Run but reads from a pre-existing message channel
// instead of calling FanIn internally. Used by the Router for lazy startup
// where the first message has already been consumed.
func RunWithMessages(ctx context.Context, opts Options, msgs <-chan channel.TaggedMessage) error {
	// Per-channel session IDs — each channel is an independent conversation.
	sessions := make(map[channel.ChannelID]string)
	for chID := range opts.Channels {
		sid, err := loadSession(ctx, opts.Store, chID)
		if err != nil {
			slog.Warn("failed to load session, starting fresh", "channel", chID, "err", err)
		}
		if sid != "" {
			sessions[chID] = sid
		}
	}

	var queue []channel.TaggedMessage
	idle := newIdleTimer()
	defer idle.Stop()

	for {
		var msg channel.TaggedMessage
		if len(queue) > 0 {
			msg = queue[0]
			queue = queue[1:]
		} else {
			select {
			case <-ctx.Done():
				return nil
			case <-idle.C():
				slog.Info("agent idle timeout, shutting down", "timeout", agentIdleTimeout)
				return ErrIdleTimeout
			case m, ok := <-msgs:
				if !ok {
					return nil
				}
				msg = m
				idle.Reset()
			}
		}

		if strings.EqualFold(msg.Text, stopKeyword) {
			continue
		}

		sessionID := sessions[msg.ChannelID]
		turnCtx, cancelTurn := context.WithCancel(ctx)

		handleDone := make(chan handleResult, 1)
		go func() {
			newSessionID, err := handle(turnCtx, opts, sessionID, msg)
			handleDone <- handleResult{sessionID: newSessionID, err: err}
		}()

		// While the turn runs, keep reading messages.
		// "stop" cancels the turn; anything else queues.
		stopped := false
		for {
			select {
			case result := <-handleDone:
				if result.err != nil && turnCtx.Err() == nil {
					slog.Error("handle failed", "err", result.err)
				}
				if result.sessionID != "" && result.sessionID != sessionID {
					slog.Info("session started", "channel", msg.ChannelID, "session_id", result.sessionID)
					sessions[msg.ChannelID] = result.sessionID
					if err := saveSession(ctx, opts.Store, msg.ChannelID, result.sessionID); err != nil {
						slog.Error("failed to save session", "err", err)
					}
				}
				goto done
			case newMsg, ok := <-msgs:
				if !ok {
					cancelTurn()
					<-handleDone
					goto done
				}
				if strings.EqualFold(newMsg.Text, stopKeyword) {
					if !stopped {
						slog.Info("turn interrupted by stop")
						cancelTurn()
						stopped = true
					}
				} else {
					queue = append(queue, newMsg)
				}
			}
		}

	done:
		cancelTurn()
		idle.Reset()
		ch, ok := opts.Channels[msg.ChannelID]
		if ok {
			if err := ch.Done(ctx); err != nil {
				slog.Error("failed to close turn", "err", err)
			}
		}
	}
}

// idleTimerT wraps a time.Timer for agent inactivity shutdown.
type idleTimerT struct {
	timer    *time.Timer
	duration time.Duration
}

func newIdleTimer() *idleTimerT {
	return &idleTimerT{timer: time.NewTimer(agentIdleTimeout), duration: agentIdleTimeout}
}

func (t *idleTimerT) C() <-chan time.Time {
	if t.timer == nil {
		return nil
	}
	return t.timer.C
}

func (t *idleTimerT) Reset() {
	if t.timer != nil {
		t.timer.Reset(t.duration)
	}
}

func (t *idleTimerT) Stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

func maxTurns(opts Options) int {
	if opts.MaxTurns > 0 {
		return opts.MaxTurns
	}
	return defaultMaxTurns
}

func buildArgs(opts Options, sessionID string, systemPrompt string, prompt string) []string {
	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--print", prompt,
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", string(opts.PermissionMode))
	}
	if opts.Model != "" {
		args = append(args, "--model", string(opts.Model))
	}
	args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns(opts)))
	for _, t := range opts.AllowedTools {
		args = append(args, "--allowedTools", string(t))
	}
	for _, t := range opts.DisallowedTools {
		args = append(args, "--disallowedTools", string(t))
	}
	return args
}
