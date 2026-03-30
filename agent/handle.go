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
	"sort"
	"strings"
	"syscall"
	"time"

	"tclaw/channel"
	"tclaw/claudecli"
)

// ErrAuthRequired is returned by handle/streamResponse when the CLI reports
// authentication_failed. The caller should start the interactive auth flow.
var ErrAuthRequired = errors.New("authentication required")

// ErrRateLimited is returned by streamResponse when the CLI's result indicates
// a rate limit error. The caller retries the turn with exponential backoff.
var ErrRateLimited = errors.New("rate limited")

// ToolsDeniedError is returned by streamResponse when the agent tried to use
// tools that aren't in the channel's allowed list. The caller can prompt the
// user and retry with the denied tools temporarily enabled.
type ToolsDeniedError struct {
	Tools     []string
	SessionID string
}

func (e *ToolsDeniedError) Error() string {
	return fmt.Sprintf("tools denied: %s", strings.Join(e.Tools, ", "))
}

// writePhase distinguishes status output (thinking, tools, stats) from
// the actual response text so they can be rendered in separate messages.
type writePhase int

const (
	phaseStatus   writePhase = iota // tool use, tool results, init, stats
	phaseResponse                   // assistant text content
	phaseThinking                   // thinking content — routed to status
)

// maxMessageLen is the threshold at which split-mode messages are rotated
// to a new message. Telegram's hard limit is 4096 chars; we use 3500 for
// safe headroom (HTML entities, emoji encoding, etc.).
const maxMessageLen = 3500

// telegramTruncateLen is the cap applied to status messages before Send/Edit.
// A single large tool output can push accumulated status content past Telegram's
// 4096-char hard limit before the proactive split fires. We truncate at 3900 to
// leave room for wrap close tags (e.g. </blockquote>) and the truncation suffix.
const telegramTruncateLen = 3900

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

	// statusSealed is set when response text first appears. The next
	// phaseStatus write starts a fresh status message so new status
	// content (later-turn thinking, tools, stats) appears below the
	// response in chat order rather than above it.
	statusSealed bool

	// respSealed is set when a new status message is sent after response
	// text. The next phaseResponse write starts a fresh response message
	// so it appears below the intervening status in chat order.
	respSealed bool

	// Status wrap tags from the channel (e.g. <blockquote expandable>).
	// Empty strings when the channel doesn't support collapsible content.
	statusWrap channel.StatusWrap
	// statusWrapOpen tracks whether we've opened the wrap tag in the
	// current status message. When true, the close tag is appended to
	// content before every send/edit so intermediate states render
	// valid markup.
	statusWrapOpen bool
}

func (tw *turnWriter) write(phase writePhase, text string) error {
	if phase == phaseThinking {
		// Route thinking to status destination.
		phase = phaseStatus
	}
	if !tw.split {
		return tw.writeSingle(text)
	}
	return tw.writeSplit(phase, text)
}

// writeSingle appends to a single message (socket, stdio).
func (tw *turnWriter) writeSingle(text string) error {
	tw.buf.WriteString(text)
	content := tw.statusSuffix(tw.buf.String())
	if tw.id == "" {
		id, err := tw.ch.Send(tw.ctx, content)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		tw.id = id
		return nil
	}
	if err := tw.ch.Edit(tw.ctx, tw.id, content); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	return nil
}

// statusSuffix appends the status wrap close tag to content when the wrap
// is open, so every intermediate send/edit renders valid markup. The close
// tag is NOT written to the buffer — it's only in the sent content.
func (tw *turnWriter) statusSuffix(content string) string {
	if tw.statusWrapOpen && tw.statusWrap.Close != "" {
		return content + tw.statusWrap.Close
	}
	return content
}

// statusPrefix prepends the status wrap open tag when the wrap is active.
// Used when message rotation resets the buffer mid-status so the new
// message starts with a valid open tag.
func (tw *turnWriter) statusPrefix(content string) string {
	if tw.statusWrap.Open != "" {
		return tw.statusWrap.Open + content
	}
	return content
}

// writeSplit routes to separate status and response messages.
// It proactively rotates to a new message when the buffer approaches
// Telegram's character limit, and reactively recovers from Edit failures
// by starting a fresh message.
func (tw *turnWriter) writeSplit(phase writePhase, text string) error {
	switch phase {
	case phaseStatus:
		// Once response text has appeared, start a fresh status message
		// so subsequent status (later-turn thinking, tools, stats) shows
		// below the response in chat order.
		if tw.statusSealed {
			tw.statusBuf.Reset()
			tw.statusID = ""
			tw.statusSealed = false
			tw.statusWrapOpen = false
		}

		// Open the status wrap on first write to this message.
		if !tw.statusWrapOpen && tw.statusWrap.Open != "" {
			text = tw.statusWrap.Open + text
			tw.statusWrapOpen = true
		}

		tw.statusBuf.WriteString(text)
		content := tw.statusSuffix(tw.statusBuf.String())

		// Proactive split: rotate to a new message before hitting the limit.
		// Re-prepend the wrap open tag so the new message has valid markup.
		if tw.statusID != "" && len(content) > maxMessageLen {
			freshText := tw.statusPrefix(text)
			tw.statusBuf.Reset()
			tw.statusBuf.WriteString(freshText)
			tw.statusID = ""
			content = tw.statusSuffix(freshText)
		}

		if tw.statusID == "" {
			id, err := tw.ch.Send(tw.ctx, truncateForTelegram(content))
			if err != nil {
				// Status is informational — log and swallow.
				slog.Warn("failed to send status message", "err", err)
				return nil
			}
			tw.statusID = id
			if tw.respID != "" {
				// A new status message was sent below the current response,
				// so the next response text must start a fresh message to
				// maintain linear chat order.
				tw.respSealed = true
			}
			return nil
		}

		if err := tw.ch.Edit(tw.ctx, tw.statusID, truncateForTelegram(content)); err != nil {
			if strings.Contains(err.Error(), "message is not modified") {
				return nil
			}
			// Reactive recovery: start a fresh message with the wrap re-opened.
			slog.Warn("failed to edit status message, starting new message", "err", err)
			freshText := tw.statusPrefix(text)
			tw.statusBuf.Reset()
			tw.statusBuf.WriteString(freshText)
			tw.statusID = ""
			recoveryContent := tw.statusSuffix(freshText)
			id, err := tw.ch.Send(tw.ctx, truncateForTelegram(recoveryContent))
			if err != nil {
				// Status is informational — log and swallow.
				slog.Warn("failed to send replacement status message", "err", err)
				return nil
			}
			tw.statusID = id
		}

	case phaseResponse:
		if tw.respSealed {
			// A status message appeared below the previous response.
			// Start fresh so the next response text is below it.
			tw.respBuf.Reset()
			tw.respID = ""
			tw.respSealed = false
		}

		// Seal the current status message so future status content
		// appears below this response in chat order.
		if tw.respID == "" {
			tw.statusSealed = true
		}
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
			if strings.Contains(err.Error(), "message is not modified") {
				return nil
			}
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
	// Resolve add-dirs fresh each turn so worktrees created mid-session
	// (via dev_start) are immediately accessible in the sandbox.
	if opts.AddDirsFunc != nil {
		opts.AddDirs = opts.AddDirsFunc()
	}

	slog.Info("handling message", "prompt_len", len(msg.Text), "channel", msg.ChannelID, "session_id", sessionID,
		"has_api_key", opts.APIKey != "", "home_dir", opts.HomeDir)

	ch, ok := opts.channels()[msg.ChannelID]
	if !ok {
		return "", fmt.Errorf("unknown channel %q", msg.ChannelID)
	}

	split := ch.SplitStatusMessages()

	tw := &turnWriter{ch: ch, ctx: ctx, split: split, statusWrap: ch.StatusWrap()}
	thinkingMsg := "🤔 Thinking...\n"
	if msg.SourceInfo != nil && msg.SourceInfo.Source == channel.SourceResume {
		thinkingMsg = "🔄 Resuming interrupted turn...\n"
	}
	if err := tw.write(phaseStatus, thinkingMsg); err != nil {
		return "", fmt.Errorf("initial write: %w", err)
	}

	// Append per-turn message context so the agent knows which channel and
	// source (user, schedule, cross-channel) this message came from.
	info := ch.Info()
	contextSection := fmt.Sprintf("\n# Message Context\n\nChannel: **%s** (%s): %s\n", info.Name, info.Type, info.Description)

	source := msg.SourceInfo
	if source == nil {
		source = &channel.MessageSourceInfo{Source: channel.SourceUser}
	}
	switch source.Source {
	case channel.SourceSchedule:
		contextSection += fmt.Sprintf("Source: scheduled prompt (%s)\n", source.ScheduleName)
	case channel.SourceChannel:
		contextSection += fmt.Sprintf("Source: cross-channel message from **%s**\n", source.FromChannel)
	case channel.SourceResume:
		contextSection += "Source: auto-resume after interrupted turn\n"
	default:
		contextSection += "Source: user message\n"
	}

	systemPrompt := opts.SystemPrompt + contextSection

	allowed, disallowed := resolveToolsForChannel(opts, msg.ChannelID)
	mcpConfigPath := resolveMCPConfigPath(opts, msg.ChannelID)
	// Log tool counts and whether the channel override was found.
	_, hasOverride := opts.ChannelToolOverrides[msg.ChannelID]
	var mcpCount int
	if opts.MCPToolNames != nil {
		mcpCount = len(opts.MCPToolNames())
	}
	slog.Debug("resolved channel tools", "channel", msg.ChannelID,
		"has_override", hasOverride, "allowed_count", len(allowed),
		"disallowed_count", len(disallowed), "mcp_tool_count", mcpCount)
	if opts.Debug {
		slog.Debug("resolved channel tools detail", "channel", msg.ChannelID,
			"allowed", allowed, "disallowed", disallowed)
	}
	args := buildArgs(opts, sessionID, systemPrompt, msg.Text, allowed, disallowed, mcpConfigPath)
	cmd := exec.CommandContext(ctx, "claude", args...)
	// Send SIGTERM on context cancel instead of the default SIGKILL, giving
	// the CLI and its Node.js child processes a chance to exit cleanly.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second
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
		if mcpConfigPath != "" {
			// The MCP config lives in mcp-config/ which is outside the user's
			// home and memory dirs. Bind its parent directory read-only so
			// the claude CLI can read --mcp-config.
			readOnly = append(readOnly, filepath.Dir(mcpConfigPath))
		}

		// Mount settings.json read-only to prevent prompt injection from
		// creating malicious CLI hooks (SessionStart) via the agent's
		// file access. The file is pre-seeded during seedUserMemory().
		settingsPath := filepath.Join(opts.HomeDir, ".claude", "settings.json")
		readOnlyOverlay := []string{settingsPath}

		readWrite := []string{opts.MemoryDir, opts.HomeDir}
		readWrite = append(readWrite, opts.AddDirs...)
		paths := sandboxPaths{
			ReadWrite:       readWrite,
			ReadOnly:        readOnly,
			ReadOnlyOverlay: readOnlyOverlay,
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

	// Raise the subprocess OOM score so the kernel kills it before tclaw.
	markSubprocessOOMTarget(cmd)

	// Drain stderr so the process doesn't block on it.
	go func() {
		data, _ := io.ReadAll(stderr)
		if len(data) > 0 {
			slog.Debug("claude stderr", "output", string(data))
		}
	}()

	newSessionID, err := streamResponse(ctx, opts, tw, stdout, allowed, msg.ChannelID)
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

	// Disable Claude Code's auto-memory so the agent only writes to
	// its own memory dir (CWD), not ~/.claude/projects/.../memory/.
	overrides["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"

	// Cap the Node.js V8 heap to stay within the VM's memory budget.
	// NODE_MAX_HEAP_MB is set in fly.toml [env] — see the comment there
	// for sizing guidance. Without this, the claude CLI can consume all
	// available memory and get OOM-killed mid-turn with no user feedback.
	if maxHeap := os.Getenv("NODE_MAX_HEAP_MB"); maxHeap != "" {
		overrides["NODE_OPTIONS"] = "--max-old-space-size=" + maxHeap
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
// allowedTools is the resolved allowed tool list for the channel — when
// non-empty, tool_use events for tools not in this list are tracked and
// returned as a ToolsDeniedError after the turn completes.
func streamResponse(ctx context.Context, opts Options, tw *turnWriter, r io.Reader, allowedTools []claudecli.Tool, channelID channel.ChannelID) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)

	var sessionID string
	var currentBlockType claudecli.ContentBlockType
	// Track whether content was streamed for the current assistant
	// message via content_block events. When true, the assistant event's
	// thinking/text blocks are redundant and should be skipped.
	gotStreamedBlocks := false
	// Track whether tool_use blocks were seen in streaming events.
	// The CLI may not stream tool_use (or may use a type we don't
	// recognize), so we extract them from the assistant event if missing.
	gotStreamedToolUse := false
	// Track whether we've already emitted a text block so we can insert
	// a newline separator before the next one.
	hadTextBlock := false

	// Track tools the model tried to use that aren't in the allowed list.
	allowedSet := make(map[claudecli.Tool]bool, len(allowedTools))
	for _, t := range allowedTools {
		allowedSet[t] = true
	}
	deniedToolSet := make(map[string]bool)
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
			gotStreamedBlocks = true
			currentBlockType = start.ContentBlock.Type
			switch currentBlockType {
			case claudecli.ContentText:
				// Separate consecutive text blocks with a newline.
				if hadTextBlock {
					if err := tw.write(phaseResponse, "\n\n"); err != nil {
						return "", err
					}
				}
			case claudecli.ContentThinking:
				if err := tw.write(phaseThinking, "💭 "); err != nil {
					return "", err
				}
			case claudecli.ContentToolUse:
				gotStreamedToolUse = true
				if err := tw.write(phaseStatus, formatToolUse(start.ContentBlock)); err != nil {
					return "", err
				}
				// Track tools the model tried to use that aren't in the allowed list.
				// Only applies when an allowlist is configured (non-empty).
				if len(allowedSet) > 0 && start.ContentBlock.Name != "" {
					toolName := claudecli.Tool(start.ContentBlock.Name)
					if !allowedSet[toolName] {
						deniedToolSet[start.ContentBlock.Name] = true
					}
				}
			default:
				slog.Debug("unhandled content block type", "type", currentBlockType)
			}

		case claudecli.EventContentBlockDelta:
			var delta claudecli.ContentDeltaEvent
			if err := json.Unmarshal(line, &delta); err != nil {
				slog.Warn("failed to parse content_block_delta", "err", err)
				continue
			}
			switch delta.Delta.Type {
			case claudecli.DeltaText:
				if err := tw.write(phaseResponse, delta.Delta.Text); err != nil {
					return "", err
				}
			case claudecli.DeltaThinking:
				if err := tw.write(phaseThinking, delta.Delta.Thinking); err != nil {
					return "", err
				}
			}

		case claudecli.EventContentBlockStop:
			switch currentBlockType {
			case claudecli.ContentText:
				hadTextBlock = true
			case claudecli.ContentThinking:
				if err := tw.write(phaseStatus, "\n"); err != nil {
					return "", err
				}
			}
			currentBlockType = ""

		case claudecli.EventAssistant:
			// The assistant event carries the complete message. When
			// streaming worked (gotStreamedBlocks), all content was
			// already sent via content_block events — skip re-emitting.
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

			if !gotStreamedBlocks {
				// Fallback: no streaming events received, extract
				// content from the full assistant message.
				fallbackHadText := false
				for _, block := range msg.Message.Content {
					text := formatBlock(block)
					if text != "" {
						phase := phaseStatus
						switch block.Type {
						case claudecli.ContentText:
							// Separate consecutive text blocks with a newline.
							if fallbackHadText {
								text = "\n\n" + text
							}
							fallbackHadText = true
							phase = phaseResponse
						case claudecli.ContentThinking:
							phase = phaseThinking
						}
						if err := tw.write(phase, text); err != nil {
							return "", err
						}
					}
				}
			} else if !gotStreamedToolUse {
				// Thinking/text were streamed but tool_use wasn't — extract
				// tool_use from the assistant event as a safety net.
				for _, block := range msg.Message.Content {
					if block.Type == claudecli.ContentToolUse {
						text := formatToolUse(block)
						if text != "" {
							if err := tw.write(phaseStatus, text); err != nil {
								return "", err
							}
						}
					}
				}
			}
			gotStreamedBlocks = false
			gotStreamedToolUse = false
			// hadTextBlock intentionally NOT reset — the response buffer
			// accumulates across assistant events, so the separator logic
			// must persist to insert \n\n between text blocks from
			// different events.

		case claudecli.EventUser:
			var user claudecli.UserEvent
			if err := json.Unmarshal(line, &user); err != nil {
				slog.Warn("failed to parse user event", "err", err)
				continue
			}
			if err := tw.write(phaseStatus, formatToolResult(user.ToolUseResult)); err != nil {
				return "", err
			}

		case claudecli.EventRateLimit:
			var rl claudecli.RateLimitEvent
			if err := json.Unmarshal(line, &rl); err != nil {
				slog.Warn("failed to parse rate_limit_event", "err", err)
				continue
			}
			if rl.RetryAfterMs <= 0 {
				// Zero retry_after is informational — the CLI retries internally.
				continue
			}
			slog.Info("rate limit event received", "retry_after_ms", rl.RetryAfterMs)
			notice := fmt.Sprintf("⏳ Rate limited — retrying in %ds...", rl.RetryAfterMs/1000)
			if err := tw.write(phaseStatus, notice+"\n"); err != nil {
				return "", err
			}

		case claudecli.EventResult:
			var result claudecli.ResultEvent
			if err := json.Unmarshal(line, &result); err != nil {
				slog.Warn("failed to parse result event", "err", err)
				continue
			}
			if result.IsError {
				if isRateLimitError(result.Result) {
					// Return the session ID so retries can resume the same session.
					return sessionID, ErrRateLimited
				}
				return "", fmt.Errorf("%s", friendlyErrorMessage(result.Result))
			}
			if result.SessionID != "" && sessionID == "" {
				sessionID = result.SessionID
			}
			stats := fmt.Sprintf("\n📊 %d turns | %.1fs | $%.4f",
				result.NumTurns,
				result.DurationMs/1000,
				result.CostUSD,
			)
			if summary := modelSummary(result.ModelUsage); summary != "" {
				stats += " " + summary
			}
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
			logArgs := []any{
				"channel", channelID,
				"turns", result.NumTurns,
				"duration_ms", result.DurationMs,
				"cost_usd", result.CostUSD,
			}
			for model, usage := range result.ModelUsage {
				logArgs = append(logArgs,
					"model."+shortModelName(model)+".input_tokens", usage.InputTokens,
					"model."+shortModelName(model)+".output_tokens", usage.OutputTokens,
					"model."+shortModelName(model)+".cost_usd", usage.CostUSD,
				)
			}
			slog.Info("turn complete", logArgs...)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanner: %w", err)
	}

	if len(deniedToolSet) > 0 {
		denied := make([]string, 0, len(deniedToolSet))
		for tool := range deniedToolSet {
			denied = append(denied, tool)
		}
		sort.Strings(denied)
		return sessionID, &ToolsDeniedError{Tools: denied, SessionID: sessionID}
	}

	return sessionID, nil
}

// modelSummary builds a parenthesized string of model short names from the
// usage map, e.g. "(opus-4.6, sonnet-4.6)". Returns empty if no models.
func modelSummary(usage map[string]claudecli.ModelUsage) string {
	if len(usage) == 0 {
		return ""
	}
	names := make([]string, 0, len(usage))
	for model := range usage {
		names = append(names, shortModelName(model))
	}
	sort.Strings(names)
	return "(" + strings.Join(names, ", ") + ")"
}

// isRateLimitError checks whether a CLI error string indicates a rate limit.
func isRateLimitError(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "429")
}

// friendlyErrorMessage converts a raw CLI error string into a user-facing message.
// Detects known error categories (rate limit, context length, auth) and returns
// a more actionable message; falls back to the raw text for unknown errors.
func friendlyErrorMessage(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "429"):
		return "rate limit reached — please wait a moment before sending another message"
	case strings.Contains(lower, "context") && (strings.Contains(lower, "length") || strings.Contains(lower, "too long") || strings.Contains(lower, "limit")):
		return "context too long — use 'compact' to reduce conversation size and try again"
	case strings.Contains(lower, "authentication") || strings.Contains(lower, "auth") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "401"):
		return "authentication error — use 'login' to re-authenticate"
	default:
		return "claude error: " + raw
	}
}

// truncateForTelegram caps s at telegramTruncateLen and appends a truncation
// notice. Applied to status messages before Send/Edit — a single large tool
// output can push accumulated content past Telegram's 4096-char hard limit
// before the proactive split has a chance to fire.
func truncateForTelegram(s string) string {
	if len(s) <= telegramTruncateLen {
		return s
	}
	return s[:telegramTruncateLen] + "…[truncated]"
}

// shortModelName converts a full model ID (possibly with context window suffix)
// into a human-friendly short name, e.g. "claude-opus-4-6[1m]" → "opus-4.6".
func shortModelName(model string) string {
	// Strip context window suffix like "[1m]".
	if idx := strings.Index(model, "["); idx >= 0 {
		model = model[:idx]
	}
	return claudecli.Model(model).ShortName()
}
