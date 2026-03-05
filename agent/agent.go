package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"

	"tclaw/channel"
	"tclaw/store"
)

const sessionKey = "session_id"
const stopKeyword = "stop"

const defaultMaxTurns = 10

// Options configures the agent and provides its dependencies.
type Options struct {
	PermissionMode PermissionMode
	Model          Model

	// MaxTurns limits agentic turns per invocation. Defaults to defaultMaxTurns.
	MaxTurns int

	Channel channel.Channel
	Store   store.Store

	// AllowedTools are auto-approved without prompting (e.g. ToolRead, ToolBash.Scoped("git *")).
	AllowedTools []Tool

	// DisallowedTools are removed from the model's context entirely.
	DisallowedTools []Tool
}

// Agent wraps the Claude Code CLI binary and connects it to a channel.
type Agent struct {
	opts Options

	// sessionID is captured from the first CLI invocation and reused
	// via --resume to maintain multi-turn conversation state.
	sessionID string
}

func New(ctx context.Context, opts Options) *Agent {
	a := &Agent{opts: opts}
	a.loadSession(ctx)
	return a
}

func (a *Agent) loadSession(ctx context.Context) {
	data, err := a.opts.Store.Get(ctx, sessionKey)
	if err != nil {
		slog.Warn("failed to load session", "err", err)
		return
	}
	if len(data) > 0 {
		a.sessionID = string(data)
		slog.Info("resumed session", "session_id", a.sessionID)
	}
}

func (a *Agent) saveSession(ctx context.Context, id string) {
	if err := a.opts.Store.Set(ctx, sessionKey, []byte(id)); err != nil {
		slog.Error("failed to save session", "err", err)
	}
}

// Run reads messages from the channel and responds until ctx is cancelled.
// Sending "stop" interrupts the active turn. Other messages queue behind it.
func (a *Agent) Run(ctx context.Context) error {
	msgs := a.opts.Channel.Messages(ctx)
	var queue []string

	for {
		// Drain the queue before waiting for new messages.
		var msg string
		if len(queue) > 0 {
			msg = queue[0]
			queue = queue[1:]
		} else {
			select {
			case <-ctx.Done():
				return nil
			case m, ok := <-msgs:
				if !ok {
					return nil
				}
				msg = m
			}
		}

		if strings.EqualFold(msg, stopKeyword) {
			continue
		}

		turnCtx, cancelTurn := context.WithCancel(ctx)

		handleDone := make(chan error, 1)
		go func() {
			handleDone <- a.handle(turnCtx, msg)
		}()

		// While the turn runs, keep reading messages.
		// "stop" cancels the turn; anything else queues.
		stopped := false
		for {
			select {
			case err := <-handleDone:
				if err != nil && turnCtx.Err() == nil {
					slog.Error("handle failed", "err", err)
				}
				goto done
			case newMsg, ok := <-msgs:
				if !ok {
					cancelTurn()
					<-handleDone
					goto done
				}
				if strings.EqualFold(newMsg, stopKeyword) {
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
		if err := a.opts.Channel.Done(ctx); err != nil {
			slog.Error("failed to close turn", "err", err)
		}
	}
}

func (a *Agent) handle(ctx context.Context, prompt string) error {
	slog.Info("handling message", "prompt", prompt, "session_id", a.sessionID)

	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--print", prompt,
	}
	if a.sessionID != "" {
		args = append(args, "--resume", a.sessionID)
	}
	if a.opts.PermissionMode != "" {
		args = append(args, "--permission-mode", string(a.opts.PermissionMode))
	}
	if a.opts.Model != "" {
		args = append(args, "--model", string(a.opts.Model))
	}
	maxTurns := a.opts.MaxTurns
	if maxTurns == 0 {
		maxTurns = defaultMaxTurns
	}
	args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	for _, t := range a.opts.AllowedTools {
		args = append(args, "--allowedTools", string(t))
	}
	for _, t := range a.opts.DisallowedTools {
		args = append(args, "--disallowedTools", string(t))
	}

	cmd := exec.CommandContext(ctx, "claude", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start claude: %w", err)
	}

	// Drain stderr so the process doesn't block on it.
	go func() {
		data, _ := io.ReadAll(stderr)
		if len(data) > 0 {
			slog.Debug("claude stderr", "output", string(data))
		}
	}()

	sessionID, err := a.streamResponse(ctx, stdout)
	if err != nil {
		return fmt.Errorf("stream response: %w", err)
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		slog.Warn("claude exited with error", "err", waitErr)
	}

	// Capture session ID from the first invocation so subsequent
	// messages resume the same conversation.
	if sessionID != "" && a.sessionID == "" {
		slog.Info("session started", "session_id", sessionID)
		a.sessionID = sessionID
		a.saveSession(ctx, sessionID)
	}

	return nil
}

// streamResponse parses stream-json events and sends them to the channel in
// real time. Returns the session ID captured from init/result events.
func (a *Agent) streamResponse(ctx context.Context, r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)

	var sessionID string
	var currentBlockType ContentBlockType

	for scanner.Scan() {
		if ctx.Err() != nil {
			return sessionID, nil
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			slog.Debug("skipping non-JSON line", "line", string(line))
			continue
		}
		slog.Debug("cli event", "type", ev.Type)

		switch ev.Type {
		case EventSystem:
			var sys SystemEvent
			if err := json.Unmarshal(line, &sys); err != nil {
				slog.Warn("failed to parse system event", "err", err)
				continue
			}
			if sys.Subtype == SystemSubtypeInit && sys.SessionID != "" {
				sessionID = sys.SessionID
			}

		case EventContentBlockStart:
			var start ContentBlockStartEvent
			if err := json.Unmarshal(line, &start); err != nil {
				slog.Warn("failed to parse content_block_start", "err", err)
				continue
			}
			currentBlockType = start.ContentBlock.Type
			switch currentBlockType {
			case ContentThinking:
				a.send(ctx, "💭 ")
			case ContentToolUse:
				a.send(ctx, fmt.Sprintf("🔧 %s\n", start.ContentBlock.Name))
			}

		case EventContentBlockDelta:
			var delta ContentDeltaEvent
			if err := json.Unmarshal(line, &delta); err != nil {
				slog.Warn("failed to parse content_block_delta", "err", err)
				continue
			}
			switch delta.Delta.Type {
			case DeltaText:
				a.send(ctx, delta.Delta.Text)
			case DeltaThinking:
				a.send(ctx, delta.Delta.Thinking)
			}

		case EventContentBlockStop:
			if currentBlockType == ContentThinking {
				a.send(ctx, "\n")
			}
			currentBlockType = ""

		case EventAssistant:
			// Already handled via delta events; skip.

		case EventUser:
			var user UserEvent
			if err := json.Unmarshal(line, &user); err != nil {
				slog.Warn("failed to parse user event", "err", err)
				continue
			}
			if user.ToolUseResult != nil {
				text := a.formatToolResult(*user.ToolUseResult)
				if text != "" {
					a.send(ctx, text)
				}
			}

		case EventResult:
			var result ResultEvent
			if err := json.Unmarshal(line, &result); err != nil {
				slog.Warn("failed to parse result event", "err", err)
				continue
			}
			if result.IsError {
				return sessionID, fmt.Errorf("claude error: %s", result.Result)
			}
			if result.SessionID != "" && sessionID == "" {
				sessionID = result.SessionID
			}
			slog.Info("turn complete",
				"turns", result.NumTurns,
				"duration_ms", result.DurationMs,
				"cost_usd", result.CostUSD,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return sessionID, fmt.Errorf("scanner: %w", err)
	}
	return sessionID, nil
}

func (a *Agent) send(ctx context.Context, text string) {
	if err := a.opts.Channel.Send(ctx, text); err != nil {
		slog.Error("failed to send", "err", err)
	}
}

func (a *Agent) formatToolResult(result ToolUseResult) string {
	out := result.Stdout
	if result.Stderr != "" {
		if out != "" {
			out += "\n"
		}
		out += result.Stderr
	}
	if out == "" {
		return ""
	}
	return "```\n" + out + "\n```\n"
}
