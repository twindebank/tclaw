package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tclaw/channel"
	"tclaw/claudecli"
)

// ErrAuthRequired is returned by handle/streamResponse when the CLI reports
// authentication_failed. The caller should start the interactive auth flow.
var ErrAuthRequired = errors.New("authentication required")

// writePhase distinguishes status output (thinking, tools, stats) from
// the actual response text so they can be rendered in separate messages.
type writePhase int

const (
	phaseStatus   writePhase = iota // thinking, tool use, tool results, init, stats
	phaseResponse                   // assistant text content
)

// maxMessageLen is the threshold at which split-mode messages are rotated
// to a new message. Telegram's hard limit is 4096 chars; we use 3500 for
// safe headroom (HTML entities, emoji encoding, etc.).
const maxMessageLen = 3500

// turnWriter accumulates output for a single turn. In split mode (Telegram),
// status and response content go to separate channel messages. In normal mode
// everything goes to one message.
type turnWriter struct {
	ch    channel.Channel
	ctx   context.Context
	split bool // true for channels that support split messages (Telegram)

	// Normal mode: single message.
	buf strings.Builder
	id  channel.MessageID

	// Split mode: two independent messages.
	statusBuf strings.Builder
	statusID  channel.MessageID
	respBuf   strings.Builder
	respID    channel.MessageID
}

func (tw *turnWriter) write(phase writePhase, text string) error {
	if !tw.split {
		return tw.writeSingle(text)
	}
	return tw.writeSplit(phase, text)
}

// writeSingle appends to a single message (socket, stdio).
func (tw *turnWriter) writeSingle(text string) error {
	tw.buf.WriteString(text)
	if tw.id == "" {
		id, err := tw.ch.Send(tw.ctx, tw.buf.String())
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		tw.id = id
		return nil
	}
	if err := tw.ch.Edit(tw.ctx, tw.id, tw.buf.String()); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	return nil
}

// writeSplit routes to separate status and response messages.
// It proactively rotates to a new message when the buffer approaches
// Telegram's character limit, and reactively recovers from Edit failures
// by starting a fresh message.
func (tw *turnWriter) writeSplit(phase writePhase, text string) error {
	switch phase {
	case phaseStatus:
		tw.statusBuf.WriteString(text)
		content := tw.statusBuf.String()

		// Proactive split: rotate to a new message before hitting the limit.
		if tw.statusID != "" && len(content) > maxMessageLen {
			tw.statusBuf.Reset()
			tw.statusBuf.WriteString(text)
			tw.statusID = ""
			content = text
		}

		if tw.statusID == "" {
			id, err := tw.ch.Send(tw.ctx, content)
			if err != nil {
				// Status is informational — log and swallow.
				slog.Warn("failed to send status message", "err", err)
				return nil
			}
			tw.statusID = id
			return nil
		}

		if err := tw.ch.Edit(tw.ctx, tw.statusID, content); err != nil {
			// Reactive recovery: start a fresh message.
			slog.Warn("failed to edit status message, starting new message", "err", err)
			tw.statusBuf.Reset()
			tw.statusBuf.WriteString(text)
			tw.statusID = ""
			id, err := tw.ch.Send(tw.ctx, text)
			if err != nil {
				// Status is informational — log and swallow.
				slog.Warn("failed to send replacement status message", "err", err)
				return nil
			}
			tw.statusID = id
		}

	case phaseResponse:
		tw.respBuf.WriteString(text)
		content := tw.respBuf.String()

		// Proactive split: rotate to a new message before hitting the limit.
		if tw.respID != "" && len(content) > maxMessageLen {
			tw.respBuf.Reset()
			tw.respBuf.WriteString(text)
			tw.respID = ""
			content = text
		}

		if tw.respID == "" {
			id, err := tw.ch.Send(tw.ctx, content)
			if err != nil {
				return fmt.Errorf("send response: %w", err)
			}
			tw.respID = id
			return nil
		}

		if err := tw.ch.Edit(tw.ctx, tw.respID, content); err != nil {
			// Reactive recovery: start a fresh message.
			slog.Warn("failed to edit response message, starting new message", "err", err)
			tw.respBuf.Reset()
			tw.respBuf.WriteString(text)
			tw.respID = ""
			id, err := tw.ch.Send(tw.ctx, text)
			if err != nil {
				return fmt.Errorf("send replacement response: %w", err)
			}
			tw.respID = id
		}
	}
	return nil
}

// handle spawns the claude CLI for a single turn and streams the response
// back to the source channel. Returns the session ID from the CLI (may be
// the same as the input sessionID or a new one from the first invocation).
func handle(ctx context.Context, opts Options, sessionID string, msg channel.TaggedMessage) (string, error) {
	slog.Info("handling message", "prompt", msg.Text, "channel", msg.ChannelID, "session_id", sessionID,
		"has_api_key", opts.APIKey != "", "home_dir", opts.HomeDir)

	ch, ok := opts.Channels[msg.ChannelID]
	if !ok {
		return "", fmt.Errorf("unknown channel %q", msg.ChannelID)
	}

	split := ch.SplitStatusMessages()

	tw := &turnWriter{ch: ch, ctx: ctx, split: split}
	if err := tw.write(phaseStatus, "🤔 Thinking...\n"); err != nil {
		return "", fmt.Errorf("initial write: %w", err)
	}

	// Append the active channel to the system prompt for this invocation.
	info := ch.Info()
	systemPrompt := opts.SystemPrompt +
		fmt.Sprintf("\n# Active Channel\n\nThis message is from **%s** (%s): %s\n", info.Name, info.Type, info.Description)

	allowed, disallowed := resolveToolsForChannel(opts, msg.ChannelID)
	if opts.Debug {
		slog.Debug("resolved channel tools", "channel", msg.ChannelID,
			"allowed", allowed, "disallowed", disallowed)
	}
	args := buildArgs(opts, sessionID, systemPrompt, msg.Text, allowed, disallowed)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = buildEnv(opts)

	// Set CWD to the memory directory so the agent's file operations
	// (Read, Write, Edit, Bash) default to the sandboxed memory dir.
	if opts.MemoryDir != "" {
		cmd.Dir = opts.MemoryDir
	} else if opts.HomeDir != "" {
		cmd.Dir = opts.HomeDir
	}

	// On Linux (deployed), wrap with bubblewrap so the subprocess can only
	// see explicitly bound paths. Locally (macOS) this is a no-op.
	if sandboxEnabled() {
		readOnly := systemReadOnlyPaths
		if opts.MCPConfigPath != "" {
			// The MCP config lives in state/ which is outside the user's
			// home and memory dirs. Bind its parent directory read-only so
			// the claude CLI can read --mcp-config.
			readOnly = append(readOnly, filepath.Dir(opts.MCPConfigPath))
		}
		readWrite := []string{opts.MemoryDir, opts.HomeDir}
		readWrite = append(readWrite, opts.AddDirs...)
		paths := sandboxPaths{
			ReadWrite: readWrite,
			ReadOnly:  readOnly,
		}
		cmd = wrapWithSandbox(ctx, cmd, paths)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude: %w", err)
	}

	// Drain stderr so the process doesn't block on it.
	go func() {
		data, _ := io.ReadAll(stderr)
		if len(data) > 0 {
			slog.Debug("claude stderr", "output", string(data))
		}
	}()

	newSessionID, err := streamResponse(ctx, opts, tw, stdout)
	if err != nil {
		return "", fmt.Errorf("stream response: %w", err)
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		slog.Warn("claude exited with error", "err", waitErr)
	}

	if newSessionID != "" {
		return newSessionID, nil
	}
	return sessionID, nil
}


// allowedEnvPrefixes are env var prefixes the subprocess is allowed to inherit.
// Everything not matching is excluded. Overrides (HOME, ANTHROPIC_API_KEY, etc.)
// are always set regardless of this list.
var allowedEnvPrefixes = []string{
	"PATH",
	"TERM",
	"COLORTERM",
	"LANG",
	"LC_",
	"TMPDIR",
	"USER",
	"LOGNAME",
	"SHELL",
	"EDITOR",
	"VISUAL",
	"XDG_",
	"TZ",
}

// buildEnv constructs the environment for the claude subprocess using an
// allowlist. Only vars matching allowedEnvPrefixes are inherited — everything
// else (cloud credentials, SSH agents, GitHub tokens, tclaw internals) is
// excluded by default. Overrides are always applied.
func buildEnv(opts Options) []string {
	overrides := make(map[string]string)
	if opts.HomeDir != "" {
		overrides["HOME"] = opts.HomeDir
	}
	if opts.APIKey != "" {
		overrides["ANTHROPIC_API_KEY"] = opts.APIKey
	}
	if opts.SetupToken != "" {
		overrides["CLAUDE_CODE_OAUTH_TOKEN"] = opts.SetupToken
	}

	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, isOverride := overrides[key]; isOverride {
			continue
		}
		if !allowedEnvVar(key) {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// allowedEnvVar returns true if the given env var name matches the allowlist.
func allowedEnvVar(key string) bool {
	for _, prefix := range allowedEnvPrefixes {
		if key == prefix || strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// streamResponse parses stream-json events and sends them to the channel in
// real time. Returns the session ID captured from init/result events.
func streamResponse(ctx context.Context, opts Options, tw *turnWriter, r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)

	var sessionID string
	var currentBlockType claudecli.ContentBlockType
	// Track whether we received text/thinking deltas for the current
	// assistant message. Reset on each assistant event so we always
	// extract content that wasn't streamed via deltas.
	gotTextDeltas := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return sessionID, nil
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev claudecli.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			slog.Debug("skipping non-JSON line", "line", string(line))
			continue
		}
		if opts.Debug {
			slog.Debug("cli event", "type", ev.Type, "json", string(line))
		}

		switch ev.Type {
		case claudecli.EventSystem:
			var sys claudecli.SystemEvent
			if err := json.Unmarshal(line, &sys); err != nil {
				slog.Warn("failed to parse system event", "err", err)
				continue
			}
			if sys.Subtype == claudecli.SystemSubtypeInit {
				if sys.SessionID != "" {
					sessionID = sys.SessionID
				}
				if err := tw.write(phaseStatus, "✅ Session ready, generating response...\n"); err != nil {
					return "", err
				}
			}

		case claudecli.EventContentBlockStart:
			var start claudecli.ContentBlockStartEvent
			if err := json.Unmarshal(line, &start); err != nil {
				slog.Warn("failed to parse content_block_start", "err", err)
				continue
			}
			currentBlockType = start.ContentBlock.Type
			switch currentBlockType {
			case claudecli.ContentThinking:
				if err := tw.write(phaseStatus, "💭 "); err != nil {
					return "", err
				}
			case claudecli.ContentToolUse:
				if err := tw.write(phaseStatus, formatToolUse(start.ContentBlock)); err != nil {
					return "", err
				}
			}

		case claudecli.EventContentBlockDelta:
			var delta claudecli.ContentDeltaEvent
			if err := json.Unmarshal(line, &delta); err != nil {
				slog.Warn("failed to parse content_block_delta", "err", err)
				continue
			}
			switch delta.Delta.Type {
			case claudecli.DeltaText:
				gotTextDeltas = true
				if err := tw.write(phaseResponse, delta.Delta.Text); err != nil {
					return "", err
				}
			case claudecli.DeltaThinking:
				gotTextDeltas = true
				if err := tw.write(phaseStatus, delta.Delta.Thinking); err != nil {
					return "", err
				}
			}

		case claudecli.EventContentBlockStop:
			if currentBlockType == claudecli.ContentThinking {
				if err := tw.write(phaseStatus, "\n"); err != nil {
					return "", err
				}
			}
			currentBlockType = ""

		case claudecli.EventAssistant:
			// The assistant event carries the complete message. Extract
			// any text/thinking that wasn't already streamed via deltas.
			var msg claudecli.AssistantEvent
			if err := json.Unmarshal(line, &msg); err != nil {
				slog.Warn("failed to parse assistant event", "err", err)
				continue
			}

			// Auth failure: suppress the CLI's error text and return
			// a sentinel so the caller can start the auth flow.
			if msg.Error == claudecli.AssistantErrorAuthFailed {
				return sessionID, ErrAuthRequired
			}

			for _, block := range msg.Message.Content {
				switch block.Type {
				case claudecli.ContentToolUse:
					// Already sent via content_block_start if available;
					// send anyway since it's idempotent for display.
				case claudecli.ContentText, claudecli.ContentThinking:
					if gotTextDeltas {
						continue
					}
				}
				text := formatBlock(block)
				if text != "" {
					phase := phaseStatus
					if block.Type == claudecli.ContentText {
						phase = phaseResponse
					}
					if err := tw.write(phase, text); err != nil {
						return "", err
					}
				}
			}
			gotTextDeltas = false

		case claudecli.EventUser:
			var user claudecli.UserEvent
			if err := json.Unmarshal(line, &user); err != nil {
				slog.Warn("failed to parse user event", "err", err)
				continue
			}
			if err := tw.write(phaseStatus, formatToolResult(user.ToolUseResult)); err != nil {
				return "", err
			}

		case claudecli.EventResult:
			var result claudecli.ResultEvent
			if err := json.Unmarshal(line, &result); err != nil {
				slog.Warn("failed to parse result event", "err", err)
				continue
			}
			if result.IsError {
				return "", fmt.Errorf("claude error: %s", result.Result)
			}
			if result.SessionID != "" && sessionID == "" {
				sessionID = result.SessionID
			}
			stats := fmt.Sprintf("\n📊 %d turns | %.1fs | $%.4f",
				result.NumTurns,
				result.DurationMs/1000,
				result.CostUSD,
			)
			maxTurns := opts.MaxTurns
			if maxTurns == 0 {
				maxTurns = defaultMaxTurns
			}
			if result.NumTurns >= maxTurns {
				stats += fmt.Sprintf("\n⚠️ Turn limit reached (%d/%d). Send another message to continue.", result.NumTurns, maxTurns)
			}
			stats += "\n"
			if err := tw.write(phaseStatus, stats); err != nil {
				return "", err
			}
			slog.Info("turn complete",
				"turns", result.NumTurns,
				"duration_ms", result.DurationMs,
				"cost_usd", result.CostUSD,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanner: %w", err)
	}
	return sessionID, nil
}
