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

	// ChannelChangeCh is closed by the router when a channel is created,
	// edited, or deleted. The agent finishes the current turn, sends a
	// restart notice, and returns ErrChannelChanged so the router can
	// rebuild channels and restart.
	ChannelChangeCh <-chan struct{}

	// QueueStore persists messages that arrive while a turn is running.
	// On startup, queued messages are restored before reading new ones.
	// Also tracks interrupted turns so the agent can resume on restart.
	// May be nil if persistence is not needed.
	QueueStore *channel.QueueStore
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

	var queue []channel.TaggedMessage

	// Restore persisted queue and resume state from a previous agent session.
	if opts.QueueStore != nil {
		qState, err := opts.QueueStore.Load(ctx)
		if err != nil {
			slog.Error("failed to load persisted queue", "err", err)
		} else {
			// Inject a resume message for the channel that was interrupted mid-turn.
			if qState.InterruptedChannel != "" {
				resumeMsg := channel.TaggedMessage{
					ChannelID:  qState.InterruptedChannel,
					Text:       "[SYSTEM: You were interrupted mid-turn. Review your conversation history and continue what you were doing. If you were waiting for user input, let the user know you're back.]",
					SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceResume},
				}
				queue = append(queue, resumeMsg)
				slog.Info("injecting resume message for interrupted channel", "channel", qState.InterruptedChannel)
			}
			for _, qm := range qState.Messages {
				queue = append(queue, channel.TaggedMessage{
					ChannelID:  qm.ChannelID,
					Text:       qm.Text,
					SourceInfo: qm.SourceInfo,
				})
			}
			if len(qState.Messages) > 0 {
				slog.Info("restored persisted queued messages", "count", len(qState.Messages))
			}
			// Clear persisted state now that it's loaded into memory.
			if err := opts.QueueStore.Save(ctx, nil); err != nil {
				slog.Error("failed to clear persisted queue after load", "err", err)
			}
			if err := opts.QueueStore.ClearInterrupted(ctx); err != nil {
				slog.Error("failed to clear interrupted marker after load", "err", err)
			}
		}
	}

	idle := newIdleTimer()
	defer idle.Stop()

	// Per-channel auth flow state so channels don't interfere with each other.
	authFlows := make(map[channel.ChannelID]*pendingAuth)

	// Per-channel reset flow state.
	resetFlows := make(map[channel.ChannelID]*pendingReset)

	// Per-channel tool approval state for when the model tries denied tools.
	toolApprovals := make(map[channel.ChannelID]*pendingToolApproval)

	// oauthNotify wakes the main loop when a background OAuth goroutine
	// finishes. It carries the channel ID that completed auth so we can
	// inject a synthetic message and let the state machine process it.
	oauthNotify := make(chan channel.ChannelID, 4)

	for {
		// restoreOverride reverts temporary tool expansions after a tool
		// approval retry. Reset each iteration; set by the approval handler.
		var restoreOverride func()

		var msg channel.TaggedMessage
		if len(queue) > 0 {
			msg = queue[0]
			queue = queue[1:]
			persistQueue(ctx, opts.QueueStore, queue)
		} else {
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
			case chID := <-oauthNotify:
				// OAuth goroutine finished. Only inject a synthetic message if
				// the auth flow still exists — it may have been cancelled by "stop".
				if _, ok := authFlows[chID]; !ok {
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
			// Stop also cancels any pending auth/reset flow for this channel.
			if flow, ok := authFlows[msg.ChannelID]; ok {
				flow.cleanup()
				delete(authFlows, msg.ChannelID)
			}
			delete(resetFlows, msg.ChannelID)
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
			if flow, ok := authFlows[msg.ChannelID]; ok {
				flow.cleanup()
				delete(authFlows, msg.ChannelID)
			}
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
			resetFlows[msg.ChannelID] = &pendingReset{state: resetChoosing}
			continue
		}

		// Handle reset flow responses (per-channel).
		if flow, ok := resetFlows[msg.ChannelID]; ok {
			ch, chOK := opts.channels()[msg.ChannelID]
			if !chOK {
				delete(resetFlows, msg.ChannelID)
				continue
			}

			switch flow.state {
			case resetChoosing:
				levels := allowedResetLevels(opts, msg.ChannelID)
				choice := strings.TrimSpace(strings.ToLower(msg.Text))
				chosen := resolveResetChoice(choice, levels)

				switch chosen {
				case ResetSession:
					old := sessions[msg.ChannelID]
					delete(sessions, msg.ChannelID)
					if opts.OnSessionUpdate != nil {
						opts.OnSessionUpdate(msg.ChannelID, "")
					}
					slog.Info("session reset", "channel", msg.ChannelID, "old_session", old)
					if _, err := ch.Send(ctx, "🗑️ Session cleared — next message starts a fresh conversation."); err != nil {
						slog.Error("failed to send reset confirmation", "err", err)
					}
					delete(resetFlows, msg.ChannelID)

				case ResetMemories, ResetProject, ResetAll:
					flow.level = chosen
					flow.state = resetConfirming
					if _, err := ch.Send(ctx, resetConfirmPrompt(chosen, ch.Markup())); err != nil {
						slog.Error("failed to send reset confirm prompt", "err", err)
					}

				case resetCancel:
					if _, err := ch.Send(ctx, "↩️ Reset cancelled."); err != nil {
						slog.Error("failed to send reset cancel", "err", err)
					}
					delete(resetFlows, msg.ChannelID)

				default:
					maxN := len(levels) + 1 // +1 for cancel
					if _, err := ch.Send(ctx, fmt.Sprintf("Please enter a number (1-%d).", maxN)); err != nil {
						slog.Error("failed to send reset re-prompt", "err", err)
					}
				}

			case resetConfirming:
				if strings.TrimSpace(strings.ToLower(msg.Text)) == "confirm" {
					slog.Info("reset confirmed", "level", resetLevelName(flow.level), "channel", msg.ChannelID)

					if opts.OnReset != nil {
						if err := opts.OnReset(flow.level); err != nil {
							slog.Error("reset failed", "level", resetLevelName(flow.level), "err", err)
							if _, sendErr := ch.Send(ctx, "❌ Reset failed: "+err.Error()); sendErr != nil {
								slog.Error("failed to send reset error", "err", sendErr)
							}
							delete(resetFlows, msg.ChannelID)
							if err := ch.Done(ctx); err != nil {
								slog.Error("failed to close turn after reset error", "err", err)
							}
							continue
						}
					}

					levelName := resetLevelName(flow.level)
					if _, err := ch.Send(ctx, "✅ "+bold(ch.Markup(), strings.ToUpper(levelName[:1])+levelName[1:])+" reset complete."); err != nil {
						slog.Error("failed to send reset confirmation", "err", err)
					}
					delete(resetFlows, msg.ChannelID)

					// Project and Everything resets require the agent to restart
					// so the router can re-seed memory and rebuild state.
					if flow.level == ResetProject || flow.level == ResetAll {
						if err := ch.Done(ctx); err != nil {
							slog.Error("failed to close turn before restart", "err", err)
						}
						return ErrResetRequested
					}
				} else {
					if _, err := ch.Send(ctx, "↩️ Reset cancelled."); err != nil {
						slog.Error("failed to send reset cancel", "err", err)
					}
					delete(resetFlows, msg.ChannelID)
				}
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
					// Channel is dead — don't register the flow.
					slog.Error("failed to send auth prompt", "err", err)
				} else {
					authFlows[msg.ChannelID] = &pendingAuth{state: authChoosing}
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

		// Handle auth flow responses (per-channel).
		if flow, ok := authFlows[msg.ChannelID]; ok {
			ch, chOK := opts.channels()[msg.ChannelID]
			if !chOK {
				delete(authFlows, msg.ChannelID)
				continue
			}

			switch flow.state {
			case authChoosing:
				choice := strings.TrimSpace(strings.ToLower(msg.Text))
				switch choice {
				case "1", "oauth":
					if !opts.Env.IsLocal() {
						m := ch.Markup()
						if _, err := ch.Send(ctx, "❌ OAuth login requires a browser and only works locally.\n"+
							"Use option "+bold(m, "2")+" to paste an API key instead.\n\n"+authPrompt(m)); err != nil {
							slog.Error("failed to send non-local oauth error", "err", err)
						}
					} else {
						if _, err := ch.Send(ctx, "⏳ Opening browser for OAuth login..."); err != nil {
							slog.Error("failed to send oauth starting message", "err", err)
						}
						startSetupToken(ctx, opts, flow, msg.ChannelID, oauthNotify)
					}
				case "2", "api", "key", "api key", "apikey":
					flow.state = authAPIKeyEntry
					if _, err := ch.Send(ctx, apiKeyPrompt(ch.Markup())); err != nil {
						slog.Error("failed to send api key prompt", "err", err)
					}
				case "3", "cancel":
					delete(authFlows, msg.ChannelID)
				default:
					m := ch.Markup()
					if _, err := ch.Send(ctx, "Please enter "+bold(m, "1")+" (OAuth) or "+bold(m, "2")+" (API key).\n\n"+authPrompt(m)); err != nil {
						slog.Error("failed to send auth re-prompt", "err", err)
					}
				}

			case authOAuthActive:
				// Check if the background OAuth goroutine has finished.
				select {
				case result := <-flow.oauthDone:
					m := ch.Markup()
					if result.setupToken != "" {
						flow.setupToken = result.setupToken
						flow.state = authDeployConfirm
						if _, err := ch.Send(ctx, "✅ "+result.loginMessage+"\n\n"+
							"Deploy setup token to production? Reply "+bold(m, "yes")+" or "+bold(m, "no")+"."); err != nil {
							slog.Error("failed to send deploy prompt", "err", err)
						}
					} else {
						if _, err := ch.Send(ctx, "❌ "+result.loginMessage); err != nil {
							slog.Error("failed to send oauth failure", "err", err)
						}
						delete(authFlows, msg.ChannelID)
					}
				default:
					// OAuth still running — tell user to wait.
					if _, err := ch.Send(ctx, "⏳ Still authenticating in browser. Send a message after you're done."); err != nil {
						slog.Error("failed to send oauth wait message", "err", err)
					}
				}

			case authDeployConfirm:
				answer := strings.TrimSpace(strings.ToLower(msg.Text))
				m := ch.Markup()
				slog.Info("deploy confirm received", "answer", answer, "user_id", opts.UserID, "token_len", len(flow.setupToken))
				switch answer {
				case "yes", "y":
					// Persist locally first so the token survives even if deploy fails.
					opts.SetupToken = flow.setupToken
					if err := persistSetupToken(ctx, opts, flow.setupToken); err != nil {
						slog.Error("failed to persist setup token", "err", err)
					} else {
						if _, sendErr := ch.Send(ctx, "✅ Token saved locally."); sendErr != nil {
							slog.Error("failed to send persist confirmation", "err", sendErr)
						}
					}

					if _, sendErr := ch.Send(ctx, "⏳ Deploying to production..."); sendErr != nil {
						slog.Error("failed to send deploy progress", "err", sendErr)
					}
					slog.Info("deploying setup token", "user_id", opts.UserID)
					if err := deploySetupToken(ctx, opts.UserID, flow.setupToken); err != nil {
						slog.Error("failed to deploy setup token", "err", err)
						if _, sendErr := ch.Send(ctx, "❌ Deploy failed: "+err.Error()); sendErr != nil {
							slog.Error("failed to send deploy error", "err", sendErr)
						}
					} else {
						if _, sendErr := ch.Send(ctx, "✅ Deployed to production."); sendErr != nil {
							slog.Error("failed to send deploy success", "err", sendErr)
						}
					}

					retryMsg := flow.originalMsg
					delete(authFlows, msg.ChannelID)
					if retryMsg.Text != "" {
						queue = append([]channel.TaggedMessage{retryMsg}, queue...)
					}
				case "no", "n", "skip":
					// Persist locally so the token survives agent restarts.
					opts.SetupToken = flow.setupToken
					if err := persistSetupToken(ctx, opts, flow.setupToken); err != nil {
						slog.Error("failed to persist setup token", "err", err)
					} else {
						if _, sendErr := ch.Send(ctx, "✅ Token saved locally."); sendErr != nil {
							slog.Error("failed to send persist confirmation", "err", sendErr)
						}
					}
					retryMsg := flow.originalMsg
					delete(authFlows, msg.ChannelID)
					if retryMsg.Text != "" {
						queue = append([]channel.TaggedMessage{retryMsg}, queue...)
					}
				default:
					if _, err := ch.Send(ctx, "Reply "+bold(m, "yes")+" to deploy or "+bold(m, "no")+" to skip."); err != nil {
						slog.Error("failed to send deploy re-prompt", "err", err)
					}
				}

			case authAPIKeyEntry:
				success := handleAPIKeyEntry(ctx, opts, ch, msg.Text)
				if success {
					opts.APIKey = strings.TrimSpace(msg.Text)
					retryMsg := flow.originalMsg
					delete(authFlows, msg.ChannelID)
					if retryMsg.Text != "" {
						queue = append([]channel.TaggedMessage{retryMsg}, queue...)
					}
				}
			}

			// Don't close the turn while OAuth is running — the goroutine
			// will deliver the result asynchronously via oauthNotify and we
			// need the connection alive to send it.
			if flow.state != authOAuthActive {
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after auth step", "err", err)
				}
			}
			continue
		}

		// Handle pending tool approval responses (per-channel).
		if approval, ok := toolApprovals[msg.ChannelID]; ok {
			ch, chOK := opts.channels()[msg.ChannelID]
			if !chOK {
				delete(toolApprovals, msg.ChannelID)
				continue
			}

			answer := strings.TrimSpace(strings.ToLower(msg.Text))
			switch answer {
			case "approve", "yes", "y":
				// Re-run the original message with denied tools temporarily allowed.
				slog.Info("tool approval granted, retrying",
					"channel", msg.ChannelID, "tools", approval.deniedTools)

				extraTools := make([]claudecli.Tool, len(approval.deniedTools))
				for i, t := range approval.deniedTools {
					extraTools[i] = claudecli.Tool(t)
				}
				sessions[msg.ChannelID] = approval.sessionID
				msg = approval.originalMsg
				delete(toolApprovals, msg.ChannelID)

				// Temporarily expand the allowed tools for this channel.
				// When a channel override exists, expand that. When it doesn't,
				// use the user-level AllowedTools as the base so we don't
				// accidentally restrict the channel to only the denied tools.
				originalOverride, hadOverride := opts.ChannelToolOverrides[msg.ChannelID]
				var base []claudecli.Tool
				if hadOverride {
					base = originalOverride.AllowedTools
				} else {
					base = opts.AllowedTools
				}
				expanded := make([]claudecli.Tool, 0, len(base)+len(extraTools))
				expanded = append(expanded, base...)
				expanded = append(expanded, extraTools...)

				override := originalOverride
				override.AllowedTools = expanded
				if opts.ChannelToolOverrides == nil {
					opts.ChannelToolOverrides = make(map[channel.ChannelID]ChannelToolPermissions)
				}
				opts.ChannelToolOverrides[msg.ChannelID] = override

				// restoreOverride reverts the temporary expansion after the turn.
				restoreOverride = func() {
					if hadOverride {
						opts.ChannelToolOverrides[msg.ChannelID] = originalOverride
					} else {
						delete(opts.ChannelToolOverrides, msg.ChannelID)
					}
				}
				// Fall through to the normal handle() dispatch below.
			default:
				// Any other message clears the pending approval and is processed normally.
				delete(toolApprovals, msg.ChannelID)
				if answer == "no" || answer == "n" || answer == "cancel" {
					if _, err := ch.Send(ctx, "↩️ Tool approval cancelled."); err != nil {
						slog.Error("failed to send approval cancel", "err", err)
					}
					if err := ch.Done(ctx); err != nil {
						slog.Error("failed to close turn after approval cancel", "err", err)
					}
					continue
				}
				// Non-cancel, non-approval: fall through to handle() with the new message.
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
		if opts.QueueStore != nil {
			if err := opts.QueueStore.SetInterrupted(ctx, msg.ChannelID); err != nil {
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
				// Auth failure: start the interactive auth flow instead of
				// showing a cryptic error. Store the original message to
				// retry once the user has authenticated.
				if errors.Is(result.err, ErrAuthRequired) {
					slog.Info("auth required, starting auth flow", "channel", msg.ChannelID)
					flow := &pendingAuth{
						state:       authChoosing,
						originalMsg: msg,
					}
					ch, chOK := opts.channels()[msg.ChannelID]
					if chOK {
						if _, sendErr := ch.Send(ctx, authPrompt(ch.Markup())); sendErr != nil {
							// Channel is dead (e.g. socket disconnected) — don't
							// leave a stale flow that blocks future messages.
							slog.Error("failed to send auth prompt, discarding flow", "err", sendErr)
							goto done
						}
					}
					authFlows[msg.ChannelID] = flow
					goto done
				}

				// Tool denial: the model tried tools not in the channel's allowed
				// list. Prompt the user to approve and retry.
				var denied *ToolsDeniedError
				if errors.As(result.err, &denied) {
					slog.Info("tools denied, prompting for approval",
						"channel", msg.ChannelID, "tools", denied.Tools)
					toolApprovals[msg.ChannelID] = &pendingToolApproval{
						originalMsg: msg,
						deniedTools: denied.Tools,
						sessionID:   denied.SessionID,
					}
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
					queue = append(queue, newMsg)
					persistQueue(ctx, opts.QueueStore, queue)
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
		// Turn completed (successfully or otherwise) — clear the interrupted
		// marker so the agent won't inject a spurious resume on next restart.
		if opts.QueueStore != nil {
			if err := opts.QueueStore.ClearInterrupted(ctx); err != nil {
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

// persistQueue saves the in-memory queue to the QueueStore. Best-effort:
// errors are logged but don't interrupt message processing.
func persistQueue(ctx context.Context, qs *channel.QueueStore, queue []channel.TaggedMessage) {
	if qs == nil {
		return
	}
	messages := make([]channel.QueuedMessage, len(queue))
	for i, m := range queue {
		messages[i] = channel.QueuedMessage{
			ChannelID:  m.ChannelID,
			Text:       m.Text,
			SourceInfo: m.SourceInfo,
			QueuedAt:   time.Now(),
		}
	}
	if err := qs.Save(ctx, messages); err != nil {
		slog.Error("failed to persist queue", "err", err)
	}
}
