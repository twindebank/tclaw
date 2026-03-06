package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"tclaw/channel"
)

// turnWriter accumulates all output for a single turn into one channel
// message, using Send for the first write and Edit for subsequent ones.
type turnWriter struct {
	ch  channel.Channel
	ctx context.Context
	buf strings.Builder
	id  channel.MessageID
}

func (tw *turnWriter) write(text string) error {
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

	tw := &turnWriter{ch: ch, ctx: ctx}
	if err := tw.write("🤔 Thinking...\n"); err != nil {
		return "", fmt.Errorf("initial write: %w", err)
	}

	// Append the active channel to the system prompt for this invocation.
	info := ch.Info()
	systemPrompt := opts.SystemPrompt +
		fmt.Sprintf("\n# Active Channel\n\nThis message is from **%s** (%s): %s\n", info.Name, info.Type, info.Description)

	args := buildArgs(opts, sessionID, systemPrompt, msg.Text)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = buildEnv(opts)

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


// buildEnv constructs the environment for the claude subprocess.
// It inherits the parent env, strips vars that block nested sessions,
// and overrides HOME and ANTHROPIC_API_KEY for per-user isolation.
func buildEnv(opts Options) []string {
	strip := map[string]bool{
		"CLAUDECODE":             true,
		"CLAUDE_CODE_ENTRYPOINT": true,
	}

	overrides := make(map[string]string)
	if opts.HomeDir != "" {
		overrides["HOME"] = opts.HomeDir
	}
	if opts.APIKey != "" {
		overrides["ANTHROPIC_API_KEY"] = opts.APIKey
	}

	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strip[key] {
			continue
		}
		if _, ok := overrides[key]; ok {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// streamResponse parses stream-json events and sends them to the channel in
// real time. Returns the session ID captured from init/result events.
func streamResponse(ctx context.Context, opts Options, tw *turnWriter, r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)

	var sessionID string
	var currentBlockType ContentBlockType
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

		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			slog.Debug("skipping non-JSON line", "line", string(line))
			continue
		}
		slog.Debug("cli event", "type", ev.Type)
		if opts.Debug {
			slog.Debug("cli event raw", "json", string(line))
		}

		switch ev.Type {
		case EventSystem:
			var sys SystemEvent
			if err := json.Unmarshal(line, &sys); err != nil {
				slog.Warn("failed to parse system event", "err", err)
				continue
			}
			if sys.Subtype == SystemSubtypeInit {
				if sys.SessionID != "" {
					sessionID = sys.SessionID
				}
				if err := tw.write("✅ Session ready, generating response...\n"); err != nil {
					return "", err
				}
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
				if err := tw.write("💭 "); err != nil {
					return "", err
				}
			case ContentToolUse:
				if err := tw.write(formatToolUse(start.ContentBlock)); err != nil {
					return "", err
				}
			}

		case EventContentBlockDelta:
			var delta ContentDeltaEvent
			if err := json.Unmarshal(line, &delta); err != nil {
				slog.Warn("failed to parse content_block_delta", "err", err)
				continue
			}
			switch delta.Delta.Type {
			case DeltaText:
				gotTextDeltas = true
				if err := tw.write(delta.Delta.Text); err != nil {
					return "", err
				}
			case DeltaThinking:
				gotTextDeltas = true
				if err := tw.write(delta.Delta.Thinking); err != nil {
					return "", err
				}
			}

		case EventContentBlockStop:
			if currentBlockType == ContentThinking {
				if err := tw.write("\n"); err != nil {
					return "", err
				}
			}
			currentBlockType = ""

		case EventAssistant:
			// The assistant event carries the complete message. Extract
			// any text/thinking that wasn't already streamed via deltas.
			var msg AssistantEvent
			if err := json.Unmarshal(line, &msg); err != nil {
				slog.Warn("failed to parse assistant event", "err", err)
				continue
			}
			for _, block := range msg.Message.Content {
				switch block.Type {
				case ContentToolUse:
					// Already sent via content_block_start if available;
					// send anyway since it's idempotent for display.
				case ContentText, ContentThinking:
					if gotTextDeltas {
						continue
					}
				}
				text := formatBlock(block)
				if text != "" {
					if err := tw.write(text); err != nil {
						return "", err
					}
				}
			}
			gotTextDeltas = false

		case EventUser:
			slog.Debug("user event raw", "json", string(line))
			var user UserEvent
			if err := json.Unmarshal(line, &user); err != nil {
				slog.Warn("failed to parse user event", "err", err)
				continue
			}
			if err := tw.write(formatToolResult(user.ToolUseResult)); err != nil {
				return "", err
			}

		case EventResult:
			var result ResultEvent
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
			if err := tw.write(fmt.Sprintf("\n📊 %d turns | %.1fs | $%.4f\n",
				result.NumTurns,
				result.DurationMs/1000,
				result.CostUSD,
			)); err != nil {
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
