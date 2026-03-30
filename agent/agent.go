package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/config"
	"tclaw/queue"
)

// SecretStore provides encrypted persistent storage for secrets.
// Matches the secret.Store interface without importing it directly.
type SecretStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
}

// secretKeyAPIKey is the key used to store the API key in the secret store.
const secretKeyAPIKey = "anthropic_api_key"

const (
	// CmdStop cancels the active turn. Typed directly by the user.
	CmdStop = "stop"

	// CmdLogin starts the OAuth login flow via `claude auth login`.
	CmdLogin = "login"

	// CmdAuthStatus shows current authentication status.
	CmdAuthStatus = "auth"

	// CmdCompact compacts the conversation context. Rewritten into a prompt
	// that asks Claude to summarize and discard verbose history.
	CmdCompact = "compact"
)

// compactPrompt is injected as the user message when the compact command is used.
const compactPrompt = "Please compact your conversation context now. Summarize the key points and discard verbose history."

// isResetCommand returns true if the raw user text is one of the
// recognised reset synonyms. Case-insensitive.
func isResetCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "new", "reset", "clear", "delete":
		return true
	}
	return false
}

const defaultMaxTurns = 10
const agentIdleTimeout = 10 * time.Minute

// Rate limit retry parameters. When the CLI returns a rate limit error,
// the turn is retried with exponential backoff.
const (
	rateLimitMaxRetries    = 3
	rateLimitInitialDelay  = 30 * time.Second
	rateLimitBackoffFactor = 2
)

// ErrIdleTimeout is returned by RunWithMessages when the agent shuts down
// due to no messages arriving within the idle timeout.
var ErrIdleTimeout = errors.New("agent idle timeout")

// ErrResetRequested is returned by RunWithMessages when a reset operation
// requires the agent to restart (project or full reset).
var ErrResetRequested = errors.New("reset requested")

// ErrChannelChanged is returned by RunWithMessages when a channel was
// created/edited/deleted and the agent needs to restart to pick up changes.
var ErrChannelChanged = errors.New("channel changed")

// ChannelToolPermissions holds per-channel tool permission overrides.
// When set, these replace (not merge with) the user-level permissions.
type ChannelToolPermissions struct {
	AllowedTools    []claudecli.Tool
	DisallowedTools []claudecli.Tool
}

// pendingToolApproval tracks a tool permission denial awaiting user confirmation.
// When the model tries a tool not in the channel's allowed list, the user is
// prompted to approve it. If approved, the original message is re-run with the
// denied tools temporarily added to the allowed list.
type pendingToolApproval struct {
	originalMsg channel.TaggedMessage
	deniedTools []string
	sessionID   string
}

// Options configures the agent. All fields are immutable after creation.
type Options struct {
	PermissionMode claudecli.PermissionMode
	Model          claudecli.Model

	// ModelFunc returns the current model to use, checked each turn.
	// Takes precedence over Model when set. Allows runtime model changes
	// via MCP tools without restarting the agent.
	ModelFunc func() claudecli.Model

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

	// MemoryDir is the agent's sandboxed workspace for persistent memory files.
	// Passed to the CLI as --add-dir and used as the subprocess CWD.
	// If empty, falls back to HomeDir for CWD and no --add-dir is passed.
	MemoryDir string

	// AddDirs are additional directories passed to the CLI as --add-dir flags
	// and added to the sandbox's read-write paths. Used for dev worktrees.
	// If AddDirsFunc is set, it takes precedence over this static list.
	AddDirs []string

	// AddDirsFunc returns the current list of additional directories to mount.
	// Called on every turn so newly created worktrees are accessible without
	// restarting the agent. Falls back to AddDirs if nil.
	AddDirsFunc func() []string

	Channels map[channel.ChannelID]channel.Channel

	// ChannelsFunc returns the live channel map. When set, takes precedence
	// over Channels for all per-message lookups, enabling the router to
	// hot-add new channels without restarting the agent. Falls back to
	// Channels when nil. Analogous to AddDirsFunc.
	ChannelsFunc func() map[channel.ChannelID]channel.Channel

	// Sessions maps channel IDs to their last-known CLI session IDs.
	// Loaded by the caller (e.g. router) from persistent storage.
	Sessions map[channel.ChannelID]string

	// OnSessionUpdate is called when a channel's session ID changes.
	// The caller (e.g. router) uses this to persist session state.
	// May be nil if persistence is not needed.
	OnSessionUpdate func(chID channel.ChannelID, sessionID string)

	// AllowedTools are auto-approved without prompting (e.g. ToolRead, ToolBash.Scoped("git *")).
	AllowedTools []claudecli.Tool

	// DisallowedTools are removed from the model's context entirely.
	DisallowedTools []claudecli.Tool

	// ChannelToolOverrides maps channel IDs to per-channel tool permissions.
	// When a channel has an override, it replaces the user-level AllowedTools
	// and DisallowedTools entirely (no merging).
	ChannelToolOverrides map[channel.ChannelID]ChannelToolPermissions

	// MCPConfigPath points to a JSON file for --mcp-config, connecting
	// Claude to the local tclaw MCP server (and any remote MCPs).
	// Empty means no MCP tools are available. Used as the default when
	// no per-channel config exists in MCPConfigPaths.
	MCPConfigPath string

	// MCPConfigPaths maps channel IDs to per-channel MCP config file paths.
	// Channels with scoped remote MCPs get their own config file containing
	// only the remote MCPs available on that channel. Channels not in this
	// map fall back to MCPConfigPath.
	MCPConfigPaths map[channel.ChannelID]string

	// MCPToolNames returns the current list of tool names registered on
	// the local MCP server (e.g. "channel_create", "schedule_list"). Called
	// on every turn to expand glob patterns in AllowedTools/DisallowedTools
	// into explicit names, since the Claude CLI's --allowedTools flag does
	// not support wildcards for MCP tools.
	// Must return live data — tools may be registered mid-session (e.g.
	// Google tools after OAuth connection).
	MCPToolNames func() []string

	// SystemPrompt is appended to the default Claude system prompt via
	// --append-system-prompt. Contains agent identity, channel context,
	// and memory instructions.
	SystemPrompt string

	// SecretStore provides encrypted storage for sensitive credentials
	// like API keys. Used by the auth flow to persist keys securely.
	// May be nil if no secret store is available (keys won't be persisted).
	SecretStore SecretStore

	// OnReset is called when the user triggers a destructive reset (memories,
	// project, or everything). The callback performs the actual filesystem
	// cleanup. May be nil if reset is not supported.
	OnReset func(level ResetLevel) error

	// OnTurnStart is called before each message is handled, with the name
	// of the channel being processed. The router uses this to track the
	// active channel for server-side validation of cross-channel sends.
	// May be nil.
	OnTurnStart func(channelName string)

	// OnTurnEnd is called after each message turn completes (whether
	// successful, failed, or stopped), with the name of the channel.
	// The router uses this to update channel activity state.
	// May be nil.
	OnTurnEnd func(channelName string)

	// Env identifies the runtime environment (e.g. "local", "prod").
	// OAuth login is only available in local environments.
	Env config.Env

	// UserID identifies the user, used for per-user credential deployment.
	UserID string

	// SetupToken is the Claude setup token for OAuth auth. When set, it is
	// passed to the subprocess as CLAUDE_CODE_OAUTH_TOKEN. On prod, loaded
	// from the per-user Fly secret; locally, captured from `claude setup-token`.
	SetupToken string

	// HasProdConfig indicates whether the config file contains a prod
	// environment section. When false, the OAuth flow skips the "deploy
	// setup token to production?" prompt.
	HasProdConfig bool

	// ChannelChangeCh is closed by the router when a channel is created,
	// edited, or deleted. The agent finishes the current turn, sends a
	// restart notice, and returns ErrChannelChanged so the router can
	// rebuild channels and restart.
	ChannelChangeCh <-chan struct{}

	// Queue is the unified priority queue for all message sources.
	// Handles persistence, source-based priority (user first), and
	// busy-channel awareness. May be nil if not needed.
	Queue *queue.Queue
}

type handleResult struct {
	sessionID string
	err       error
}

// channels returns the current channel map. Uses ChannelsFunc when set for
// live updates (e.g. hot-added channels); falls back to the static Channels map.
func (opts Options) channels() map[channel.ChannelID]channel.Channel {
	if opts.ChannelsFunc != nil {
		return opts.ChannelsFunc()
	}
	return opts.Channels
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
	// Load persisted credentials if none were provided via config/env.
	if opts.APIKey == "" {
		if key := loadPersistedAPIKey(ctx, opts); key != "" {
			opts.APIKey = key
			slog.Info("loaded persisted API key")
		}
	}
	if opts.SetupToken == "" {
		if token := loadPersistedSetupToken(ctx, opts); token != "" {
			opts.SetupToken = token
			slog.Info("loaded persisted setup token")
		}
	}

	// Per-channel session IDs — seeded from opts, updated as sessions change.
	sessions := make(map[channel.ChannelID]string)
	for chID, sid := range opts.Sessions {
		if sid != "" {
			sessions[chID] = sid
		}
	}

	// Restore persisted queue and resume state from a previous agent session.
	if opts.Queue != nil {
		if err := opts.Queue.LoadPersisted(ctx); err != nil {
			slog.Error("failed to load persisted queue", "err", err)
		}
		if interruptedCh := opts.Queue.InterruptedChannel(); interruptedCh != "" {
			// Inject a resume message so the agent continues where it left off.
			resumeMsg := channel.TaggedMessage{
				ChannelID:  interruptedCh,
				Text:       "[SYSTEM: You were interrupted mid-turn. Review your conversation history and continue what you were doing. If you were waiting for user input, let the user know you're back.]",
				SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceResume},
			}
			if pushErr := opts.Queue.Push(ctx, resumeMsg); pushErr != nil {
				slog.Error("failed to push resume message", "err", pushErr)
			}
			slog.Info("injecting resume message for interrupted channel", "channel", interruptedCh)
			if clearErr := opts.Queue.ClearInterrupted(ctx); clearErr != nil {
				slog.Error("failed to clear interrupted marker after load", "err", clearErr)
			}
		}
		if opts.Queue.Len() > 0 {
			slog.Info("restored persisted queued messages", "count", opts.Queue.Len())
		}
	}

	idle := newIdleTimer()
	defer idle.Stop()

	// FlowManager tracks all per-channel interactive flows (auth, reset,
	// tool approval) in one place with explicit typed states.
	fm := NewFlowManager()

	for {
		// restoreOverride reverts temporary tool expansions after a tool
		// approval retry. Reset each iteration; set by the approval handler.
		var restoreOverride func()

		// Wait for the next processable message. User/resume messages dequeue
		// immediately; non-user messages wait until the target channel is idle.
		var msg channel.TaggedMessage
		if opts.Queue != nil {
			// Use the priority queue — blocks until a message is processable.
			// We still need to handle ChannelChangeCh, idle timeout, and OAuth
			// notifications, so use a cancellable context for the Next() call.
			type nextResult struct {
				msg channel.TaggedMessage
				err error
			}
			nextCtx, nextCancel := context.WithCancel(ctx)
			nextCh := make(chan nextResult, 1)
			go func() {
				m, err := opts.Queue.Next(nextCtx, msgs)
				nextCh <- nextResult{m, err}
			}()

			select {
			case <-opts.ChannelChangeCh:
				nextCancel()
				return ErrChannelChanged
			case <-idle.C():
				nextCancel()
				slog.Info("agent idle timeout, shutting down", "timeout", agentIdleTimeout)
				return ErrIdleTimeout
			case chID := <-fm.OAuthNotify:
				nextCancel()
				<-nextCh // wait for goroutine to exit before continuing
				if !fm.HasFlow(chID, FlowAuth) {
					continue
				}
				msg = channel.TaggedMessage{ChannelID: chID}
			case result := <-nextCh:
				nextCancel()
				if result.err != nil {
					if errors.Is(result.err, context.Canceled) {
						return nil
					}
					if errors.Is(result.err, queue.ErrInputClosed) {
						return nil
					}
					return result.err
				}
				msg = result.msg
				idle.Reset()
			}
		} else {
			// Fallback without queue (e.g. tests).
			select {
			case <-ctx.Done():
				return nil
			case <-opts.ChannelChangeCh:
				return ErrChannelChanged
			case <-idle.C():
				slog.Info("agent idle timeout, shutting down", "timeout", agentIdleTimeout)
				return ErrIdleTimeout
			case m, ok := <-msgs:
				if !ok {
					return nil
				}
				msg = m
				idle.Reset()
			case chID := <-fm.OAuthNotify:
				if !fm.HasFlow(chID, FlowAuth) {
					continue
				}
				msg = channel.TaggedMessage{ChannelID: chID}
			}
		}

		// Notify the user when a message arrives from another channel, so
		// it's clear what triggered the agent turn before the response appears.
		if msg.SourceInfo != nil && msg.SourceInfo.Source == channel.SourceChannel {
			if ch, ok := opts.channels()[msg.ChannelID]; ok {
				notification := fmt.Sprintf("↩️ Message from %s channel", msg.SourceInfo.FromChannel)
				if _, err := ch.Send(ctx, notification); err != nil {
					slog.Error("failed to send cross-channel notification", "channel", msg.ChannelID, "err", err)
				}
			}
		}

		if strings.EqualFold(msg.Text, CmdStop) {
			if !isBuiltinAllowed(opts, msg.ChannelID, claudecli.BuiltinStop) {
				sendDenied(ctx, opts, msg.ChannelID)
				continue
			}
			// Stop also cancels any pending flow for this channel.
			fm.Cancel(msg.ChannelID)
			continue
		}

		// Compact: rewrite the message into a prompt and fall through to
		// normal handling so it works on all channels.
		if strings.EqualFold(msg.Text, CmdCompact) {
			if !isBuiltinAllowed(opts, msg.ChannelID, claudecli.BuiltinCompact) {
				sendDenied(ctx, opts, msg.ChannelID)
				continue
			}
			if ch, ok := opts.channels()[msg.ChannelID]; ok {
				if _, err := ch.Send(ctx, "🗜️ Compacting conversation context..."); err != nil {
					slog.Error("failed to send compact notice", "err", err)
				}
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after compact notice", "err", err)
				}
			}
			msg.Text = compactPrompt
			// Fall through to handle() below.
		}

		// Reset command: show the multi-option reset menu.
		// Always allowed — allowedResetLevels guarantees at least Session.
		if isResetCommand(msg.Text) {
			fm.Cancel(msg.ChannelID)
			ch, ok := opts.channels()[msg.ChannelID]
			if ok {
				levels := allowedResetLevels(opts, msg.ChannelID)
				if _, err := ch.Send(ctx, dynamicResetMenuPrompt(levels, ch.Markup())); err != nil {
					slog.Error("failed to send reset menu", "err", err)
				}
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after reset menu", "err", err)
				}
			}
			fm.StartReset(msg.ChannelID)
			continue
		}

		// Handle active reset flow.
		if f := fm.Active(msg.ChannelID); f != nil && f.Kind == FlowReset {
			ch, chOK := opts.channels()[msg.ChannelID]
			if !chOK {
				fm.Complete(msg.ChannelID)
				continue
			}
			result := handleResetFlow(ctx, opts, fm, f.Reset, ch, msg, sessions)
			if result.RestartAgent != nil {
				return result.RestartAgent
			}
			if err := ch.Done(ctx); err != nil {
				slog.Error("failed to close turn after reset step", "err", err)
			}
			continue
		}

		// Explicit auth commands.
		if strings.EqualFold(msg.Text, CmdLogin) {
			if !isBuiltinAllowed(opts, msg.ChannelID, claudecli.BuiltinLogin) {
				sendDenied(ctx, opts, msg.ChannelID)
				continue
			}
			ch, ok := opts.channels()[msg.ChannelID]
			if ok {
				if _, err := ch.Send(ctx, authPrompt(ch.Markup())); err != nil {
					slog.Error("failed to send auth prompt", "err", err)
				} else {
					fm.StartAuth(msg.ChannelID, channel.TaggedMessage{})
				}
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after login prompt", "err", err)
				}
			}
			continue
		}
		if strings.EqualFold(msg.Text, CmdAuthStatus) {
			if !isBuiltinAllowed(opts, msg.ChannelID, claudecli.BuiltinAuth) {
				sendDenied(ctx, opts, msg.ChannelID)
				continue
			}
			ch, ok := opts.channels()[msg.ChannelID]
			if ok {
				handleAuthStatus(ctx, opts, ch)
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after auth status", "err", err)
				}
			}
			continue
		}

		// Handle active auth flow.
		if f := fm.Active(msg.ChannelID); f != nil && f.Kind == FlowAuth {
			ch, chOK := opts.channels()[msg.ChannelID]
			if !chOK {
				fm.Complete(msg.ChannelID)
				continue
			}
			result := handleAuthFlow(ctx, opts, fm, f.Auth, ch, msg)
			for _, retryMsg := range result.RetryMessages {
				if opts.Queue != nil {
					if pushErr := opts.Queue.Push(ctx, retryMsg); pushErr != nil {
						slog.Error("failed to push auth retry message", "err", pushErr)
					}
				}
			}
			// Don't close the turn while OAuth is running.
			if f.Auth == nil || f.Auth.state != authOAuthActive {
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after auth step", "err", err)
				}
			}
			continue
		}

		// Handle active tool approval flow.
		if f := fm.Active(msg.ChannelID); f != nil && f.Kind == FlowToolApproval {
			ch, chOK := opts.channels()[msg.ChannelID]
			if !chOK {
				fm.Complete(msg.ChannelID)
				continue
			}
			result := handleToolApprovalFlow(ctx, opts, fm, f.ToolApproval, ch, msg, sessions)
			if result.RestoreFunc != nil {
				restoreOverride = result.RestoreFunc
			}
			if result.FallThroughMsg != nil {
				msg = *result.FallThroughMsg
				// Fall through to handle() below.
			} else if result.Handled {
				continue
			}
		}

		sessionID := sessions[msg.ChannelID]
		turnCtx, cancelTurn := context.WithCancel(ctx)

		if opts.OnTurnStart != nil {
			if ch, ok := opts.channels()[msg.ChannelID]; ok {
				opts.OnTurnStart(ch.Info().Name)
			}
		}
		// Mark which channel is being processed so we can inject a resume
		// message if the agent is interrupted before the turn completes.
		if opts.Queue != nil {
			if err := opts.Queue.SetInterrupted(ctx, msg.ChannelID); err != nil {
				slog.Error("failed to set interrupted channel", "err", err)
			}
		}

		handleDone := make(chan handleResult, 1)
		go func() {
			currentSessionID := sessionID
			delay := rateLimitInitialDelay

			for attempt := 0; attempt <= rateLimitMaxRetries; attempt++ {
				newSessionID, err := handle(turnCtx, opts, currentSessionID, msg)
				if newSessionID != "" {
					currentSessionID = newSessionID
				}

				if !errors.Is(err, ErrRateLimited) || attempt == rateLimitMaxRetries {
					handleDone <- handleResult{sessionID: currentSessionID, err: err}
					return
				}

				slog.Info("rate limited, scheduling retry",
					"attempt", attempt+1, "max_retries", rateLimitMaxRetries,
					"delay", delay, "channel", msg.ChannelID)

				if retryCh, ok := opts.channels()[msg.ChannelID]; ok {
					notice := fmt.Sprintf("⏳ Rate limited — retrying in %ds (attempt %d/%d)...",
						int(delay.Seconds()), attempt+1, rateLimitMaxRetries)
					if _, sendErr := retryCh.Send(turnCtx, notice); sendErr != nil {
						slog.Error("failed to send retry notice", "err", sendErr)
					}
				}

				select {
				case <-turnCtx.Done():
					handleDone <- handleResult{sessionID: currentSessionID, err: turnCtx.Err()}
					return
				case <-time.After(delay):
				}
				delay *= time.Duration(rateLimitBackoffFactor)
			}
		}()

		// While the turn runs, keep reading messages.
		// "stop" cancels the turn; anything else queues.
		stopped := false
		for {
			select {
			case result := <-handleDone:
				// Auth failure: start the interactive auth flow.
				if errors.Is(result.err, ErrAuthRequired) {
					slog.Info("auth required, starting auth flow", "channel", msg.ChannelID)
					fm.StartAuth(msg.ChannelID, msg)
					ch, chOK := opts.channels()[msg.ChannelID]
					if chOK {
						if _, sendErr := ch.Send(ctx, authPrompt(ch.Markup())); sendErr != nil {
							slog.Error("failed to send auth prompt, discarding flow", "err", sendErr)
							fm.Cancel(msg.ChannelID)
							goto done
						}
					}
					goto done
				}

				// Tool denial: prompt for approval.
				var denied *ToolsDeniedError
				if errors.As(result.err, &denied) {
					slog.Info("tools denied, prompting for approval",
						"channel", msg.ChannelID, "tools", denied.Tools)
					fm.StartToolApproval(msg.ChannelID, msg, denied.Tools, denied.SessionID)
					if approvalCh, chOK := opts.channels()[msg.ChannelID]; chOK {
						m := approvalCh.Markup()
						toolList := strings.Join(denied.Tools, ", ")
						prompt := fmt.Sprintf("⚠️ %s was not available on this channel.\nReply %s to retry with %s enabled, or send any other message to continue.",
							bold(m, toolList), bold(m, "approve"), bold(m, toolList))
						if _, sendErr := approvalCh.Send(ctx, prompt); sendErr != nil {
							slog.Error("failed to send tool approval prompt", "err", sendErr)
						}
					}
					goto done
				}

				if result.err != nil && turnCtx.Err() == nil {
					// Turn failed unexpectedly (not cancelled by user). Notify
					// the channel so the user knows — especially important for
					// scheduled turns where nobody is watching.
					slog.Error("handle failed", "err", result.err)
					if errCh, errChOK := opts.channels()[msg.ChannelID]; errChOK {
						errText := "⚠️ Turn failed: " + result.err.Error()
						if _, sendErr := errCh.Send(ctx, errText); sendErr != nil {
							slog.Error("failed to send error notification", "err", sendErr)
						}
					}
				}
				if result.sessionID != "" && result.sessionID != sessionID {
					slog.Info("session started", "channel", msg.ChannelID, "session_id", result.sessionID)
					sessions[msg.ChannelID] = result.sessionID
					if opts.OnSessionUpdate != nil {
						opts.OnSessionUpdate(msg.ChannelID, result.sessionID)
					}
				}
				goto done
			case newMsg, ok := <-msgs:
				if !ok {
					cancelTurn()
					<-handleDone
					goto done
				}
				if strings.EqualFold(newMsg.Text, CmdStop) {
					if !stopped {
						slog.Info("turn interrupted by stop")
						cancelTurn()
						stopped = true
					}
				} else {
					if opts.Queue != nil {
						if pushErr := opts.Queue.Push(ctx, newMsg); pushErr != nil {
							slog.Error("failed to push to queue during turn", "err", pushErr)
						}
					}
					// Acknowledge the queued message so the user knows it was received.
					if queueCh, ok := opts.channels()[newMsg.ChannelID]; ok {
						ackMsg := "📥 Queued — will process after the current turn finishes."
						if newMsg.ChannelID != msg.ChannelID {
							// Show which channel is busy so the user knows what's blocking.
							if busyCh, busyOK := opts.channels()[msg.ChannelID]; busyOK {
								ackMsg = fmt.Sprintf("📥 Queued — agent is busy on %s. Will process once the current turn finishes.", busyCh.Info().Name)
							}
						}
						if _, sendErr := queueCh.Send(ctx, ackMsg); sendErr != nil {
							slog.Error("failed to send queue acknowledgment", "channel", newMsg.ChannelID, "err", sendErr)
						}
						// Only call Done() if the queued message is on a different channel.
						// Calling Done() on the active turn's channel (e.g. a socket) would
						// close the connection mid-turn.
						if newMsg.ChannelID != msg.ChannelID {
							if doneErr := queueCh.Done(ctx); doneErr != nil {
								slog.Error("failed to close turn after queue acknowledgment", "channel", newMsg.ChannelID, "err", doneErr)
							}
						}
					}
				}
			}
		}

	done:
		cancelTurn()
		if restoreOverride != nil {
			restoreOverride()
		}
		// Only clear the interrupted marker if the turn completed normally.
		// If the parent context was cancelled (deploy, shutdown), preserve
		// the marker so the agent can resume the interrupted turn on restart.
		if opts.Queue != nil && ctx.Err() == nil {
			if err := opts.Queue.ClearInterrupted(ctx); err != nil {
				slog.Error("failed to clear interrupted channel", "err", err)
			}
		}
		if opts.OnTurnEnd != nil {
			if ch, ok := opts.channels()[msg.ChannelID]; ok {
				opts.OnTurnEnd(ch.Info().Name)
			}
		}
		idle.Reset()
		ch, ok := opts.channels()[msg.ChannelID]
		if ok {
			if err := ch.Done(ctx); err != nil {
				slog.Error("failed to close turn", "err", err)
			}
		}

		// Check if a channel change happened during this turn.
		// The router closed ChannelChangeCh instead of killing us,
		// so we get to finish the turn and send a notice first.
		if opts.ChannelChangeCh != nil {
			select {
			case <-opts.ChannelChangeCh:
				if ch != nil {
					if _, err := ch.Send(ctx, "🔄 Restarting to apply channel changes..."); err != nil {
						slog.Error("failed to send restart notice", "err", err)
					}
					if err := ch.Done(ctx); err != nil {
						slog.Error("failed to close turn after restart notice", "err", err)
					}
				}
				return ErrChannelChanged
			default:
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

// isBuiltinAllowed checks whether a builtin command is permitted on the given channel.
// If the channel has overrides, checks those; otherwise checks user-level tools.
// If NO builtin entries exist at all (neither channel nor user level), everything
// is allowed for backwards compatibility.
func isBuiltinAllowed(opts Options, channelID channel.ChannelID, cmd claudecli.Tool) bool {
	var tools []claudecli.Tool
	if override, ok := opts.ChannelToolOverrides[channelID]; ok {
		tools = override.AllowedTools
	} else {
		tools = opts.AllowedTools
	}

	// If no builtin entries exist at all, allow everything (backwards compat).
	hasAnyBuiltin := false
	for _, t := range tools {
		if claudecli.IsBuiltinTool(t) {
			hasAnyBuiltin = true
			break
		}
	}
	if !hasAnyBuiltin {
		return true
	}

	for _, t := range tools {
		if t == cmd {
			return true
		}
		// builtin__reset acts as wildcard for all reset sub-levels.
		if t == claudecli.BuiltinReset && isResetBuiltin(cmd) {
			return true
		}
	}
	return false
}

// isResetBuiltin reports whether cmd is any of the reset builtins.
func isResetBuiltin(cmd claudecli.Tool) bool {
	switch cmd {
	case claudecli.BuiltinReset, claudecli.BuiltinResetSession, claudecli.BuiltinResetMemories,
		claudecli.BuiltinResetProject, claudecli.BuiltinResetAll:
		return true
	}
	return false
}

// builtinDeniedMessage is sent when a builtin command is not allowed on the current channel.
const builtinDeniedMessage = "⛔ This command is not available on this channel."

// sendDenied sends the denial message and closes the turn.
func sendDenied(ctx context.Context, opts Options, channelID channel.ChannelID) {
	ch, ok := opts.channels()[channelID]
	if !ok {
		return
	}
	if _, err := ch.Send(ctx, builtinDeniedMessage); err != nil {
		slog.Error("failed to send denied message", "err", err)
	}
	if err := ch.Done(ctx); err != nil {
		slog.Error("failed to close turn after denied message", "err", err)
	}
}

func maxTurns(opts Options) int {
	if opts.MaxTurns > 0 {
		return opts.MaxTurns
	}
	return defaultMaxTurns
}

// resolveToolsForChannel returns the allowed and disallowed tool lists for a
// specific channel. If the channel has overrides, they replace the user-level
// lists entirely. Builtin tools (builtin__*) are filtered out since the CLI
// doesn't understand them. MCP glob patterns are expanded into explicit tool
// names since the CLI's --allowedTools flag doesn't support wildcards for MCP
// tools.
func resolveToolsForChannel(opts Options, channelID channel.ChannelID) (allowed []claudecli.Tool, disallowed []claudecli.Tool) {
	if override, ok := opts.ChannelToolOverrides[channelID]; ok {
		allowed = filterOutBuiltins(override.AllowedTools)
		disallowed = filterOutBuiltins(override.DisallowedTools)
	} else {
		allowed = filterOutBuiltins(opts.AllowedTools)
		disallowed = filterOutBuiltins(opts.DisallowedTools)
	}
	var mcpToolNames []string
	if opts.MCPToolNames != nil {
		mcpToolNames = opts.MCPToolNames()
	}
	allowed = expandMCPGlobs(allowed, mcpToolNames)
	disallowed = expandMCPGlobs(disallowed, mcpToolNames)
	return allowed, disallowed
}

// expandMCPGlobs replaces glob patterns (containing * or ?) in the tool list
// with the matching MCP tool names. Non-glob entries are passed through as-is.
// The Claude CLI's --allowedTools flag doesn't support wildcards for MCP tools,
// so we expand "mcp__tclaw__channel_*" into "mcp__tclaw__channel_create",
// "mcp__tclaw__channel_edit", etc.
func expandMCPGlobs(tools []claudecli.Tool, mcpToolNames []string) []claudecli.Tool {
	if len(mcpToolNames) == 0 {
		return tools
	}

	// Build the set of fully-qualified MCP tool names (mcp__tclaw__<name>)
	// that the CLI will see.
	qualified := make([]string, len(mcpToolNames))
	for i, name := range mcpToolNames {
		qualified[i] = "mcp__tclaw__" + name
	}

	var out []claudecli.Tool
	for _, t := range tools {
		ts := string(t)
		if !strings.ContainsAny(ts, "*?[") {
			// Not a glob — keep as-is.
			out = append(out, t)
			continue
		}
		// Expand the glob against known MCP tool names.
		matched := false
		for _, q := range qualified {
			if ok, _ := filepath.Match(ts, q); ok {
				out = append(out, claudecli.Tool(q))
				matched = true
			}
		}
		if !matched {
			// No MCP tools matched — keep the original pattern so it can
			// still match non-MCP tools (e.g. "Bash*").
			out = append(out, t)
		}
	}
	return out
}

// filterOutBuiltins returns a copy of tools with all builtin__* entries removed.
func filterOutBuiltins(tools []claudecli.Tool) []claudecli.Tool {
	var out []claudecli.Tool
	for _, t := range tools {
		if !claudecli.IsBuiltinTool(t) {
			out = append(out, t)
		}
	}
	return out
}

// resolveMCPConfigPath returns the MCP config file path for the given channel.
// Per-channel configs take priority, then the default config path.
func resolveMCPConfigPath(opts Options, channelID channel.ChannelID) string {
	if p, ok := opts.MCPConfigPaths[channelID]; ok {
		return p
	}
	return opts.MCPConfigPath
}

func buildArgs(opts Options, sessionID string, systemPrompt string, prompt string, allowed []claudecli.Tool, disallowed []claudecli.Tool, mcpConfigPath string) []string {
	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--print",
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
	model := opts.Model
	if opts.ModelFunc != nil {
		if m := opts.ModelFunc(); m != "" {
			model = m
		}
	}
	if model != "" {
		args = append(args, "--model", string(model))
	}
	args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns(opts)))
	for _, t := range allowed {
		args = append(args, "--allowedTools", string(t))
	}
	for _, t := range disallowed {
		args = append(args, "--disallowedTools", string(t))
	}
	if mcpConfigPath != "" {
		args = append(args, "--mcp-config", mcpConfigPath)
	}
	if opts.MemoryDir != "" {
		args = append(args, "--add-dir", opts.MemoryDir)
	}
	for _, d := range opts.AddDirs {
		args = append(args, "--add-dir", d)
	}
	// "--" terminates flag parsing so prompts starting with "-" aren't
	// mistaken for CLI options.
	args = append(args, "--", prompt)
	return args
}
