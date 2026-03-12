package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tclaw/channel"
	"tclaw/claudecli"
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

	// CmdReset clears the channel's session so the next message starts fresh.
	// Sent by the chat client when the user types "new", "reset", "clear", or "delete".
	CmdReset = "/tclaw:reset"

	// CmdLogin starts the OAuth login flow via `claude auth login`.
	CmdLogin = "login"

	// CmdAuthStatus shows current authentication status.
	CmdAuthStatus = "auth"

	// "compact" is handled client-side — rewritten into a prompt asking Claude
	// to compact its conversation context. Not defined here since the agent
	// never sees it.
)

// isResetCommand returns true if the raw user text is one of the
// recognised reset synonyms. Case-insensitive.
func isResetCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "new", "reset", "clear", "delete", CmdReset:
		return true
	}
	return false
}

const defaultMaxTurns = 10
const agentIdleTimeout = 10 * time.Minute

// ErrIdleTimeout is returned by RunWithMessages when the agent shuts down
// due to no messages arriving within the idle timeout.
var ErrIdleTimeout = errors.New("agent idle timeout")

// Options configures the agent. All fields are immutable after creation.
type Options struct {
	PermissionMode claudecli.PermissionMode
	Model          claudecli.Model

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

	Channels map[channel.ChannelID]channel.Channel

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

	// MCPConfigPath points to a JSON file for --mcp-config, connecting
	// Claude to the local tclaw MCP server (and any remote MCPs).
	// Empty means no MCP tools are available.
	MCPConfigPath string

	// SystemPrompt is appended to the default Claude system prompt via
	// --append-system-prompt. Contains agent identity, channel context,
	// and memory instructions.
	SystemPrompt string

	// SecretStore provides encrypted storage for sensitive credentials
	// like API keys. Used by the auth flow to persist keys securely.
	// May be nil if no secret store is available (keys won't be persisted).
	SecretStore SecretStore

	// Env identifies the runtime environment (e.g. "local", "prod").
	// OAuth login is only available when Env == "local".
	Env string

	// UserID identifies the user, used for per-user credential deployment.
	UserID string

	// SetupToken is the Claude setup token for OAuth auth. When set, it is
	// passed to the subprocess as CLAUDE_CODE_OAUTH_TOKEN. On prod, loaded
	// from the per-user Fly secret; locally, captured from `claude setup-token`.
	SetupToken string
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
	idle := newIdleTimer()
	defer idle.Stop()

	// Per-channel auth flow state so channels don't interfere with each other.
	authFlows := make(map[channel.ChannelID]*pendingAuth)

	// oauthNotify wakes the main loop when a background OAuth goroutine
	// finishes. It carries the channel ID that completed auth so we can
	// inject a synthetic message and let the state machine process it.
	oauthNotify := make(chan channel.ChannelID, 4)

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
			case chID := <-oauthNotify:
				// OAuth goroutine finished. Only inject a synthetic message if
				// the auth flow still exists — it may have been cancelled by "stop".
				if _, ok := authFlows[chID]; !ok {
					continue
				}
				msg = channel.TaggedMessage{ChannelID: chID}
			}
		}

		if strings.EqualFold(msg.Text, CmdStop) {
			// Stop also cancels any pending auth flow for this channel.
			if flow, ok := authFlows[msg.ChannelID]; ok {
				flow.cleanup()
				delete(authFlows, msg.ChannelID)
			}
			continue
		}

		// Control command: clear session so next message starts fresh.
		// The chat client sends `/tclaw:reset`, but other channels (Telegram)
		// send the raw word. Normalise both forms here.
		if isResetCommand(msg.Text) {
			if flow, ok := authFlows[msg.ChannelID]; ok {
				flow.cleanup()
				delete(authFlows, msg.ChannelID)
			}
			old := sessions[msg.ChannelID]
			delete(sessions, msg.ChannelID)
			if opts.OnSessionUpdate != nil {
				opts.OnSessionUpdate(msg.ChannelID, "")
			}
			slog.Info("session reset", "channel", msg.ChannelID, "old_session", old)
			ch, ok := opts.Channels[msg.ChannelID]
			if ok {
				if _, err := ch.Send(ctx, "🗑️ session cleared — next message starts a fresh conversation."); err != nil {
					slog.Error("failed to send reset confirmation", "err", err)
				}
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after reset", "err", err)
				}
			}
			continue
		}

		// Explicit auth commands.
		if strings.EqualFold(msg.Text, CmdLogin) {
			ch, ok := opts.Channels[msg.ChannelID]
			if ok {
				if _, err := ch.Send(ctx, authPrompt(ch.Markup())); err != nil {
					slog.Error("failed to send auth prompt", "err", err)
				}
				if err := ch.Done(ctx); err != nil {
					slog.Error("failed to close turn after login prompt", "err", err)
				}
				authFlows[msg.ChannelID] = &pendingAuth{state: authChoosing}
			}
			continue
		}
		if strings.EqualFold(msg.Text, CmdAuthStatus) {
			ch, ok := opts.Channels[msg.ChannelID]
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
			ch, chOK := opts.Channels[msg.ChannelID]
			if !chOK {
				delete(authFlows, msg.ChannelID)
				continue
			}

			switch flow.state {
			case authChoosing:
				choice := strings.TrimSpace(strings.ToLower(msg.Text))
				switch choice {
				case "1", "oauth":
					if opts.Env != "local" && opts.Env != "" {
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
				// Auth failure: start the interactive auth flow instead of
				// showing a cryptic error. Store the original message to
				// retry once the user has authenticated.
				if errors.Is(result.err, ErrAuthRequired) {
					slog.Info("auth required, starting auth flow", "channel", msg.ChannelID)
					authFlows[msg.ChannelID] = &pendingAuth{
						state:       authChoosing,
						originalMsg: msg,
					}
					ch, chOK := opts.Channels[msg.ChannelID]
					if chOK {
						if _, sendErr := ch.Send(ctx, authPrompt(ch.Markup())); sendErr != nil {
							slog.Error("failed to send auth prompt", "err", sendErr)
						}
					}
					goto done
				}

				if result.err != nil && turnCtx.Err() == nil {
					slog.Error("handle failed", "err", result.err)
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
	if opts.MCPConfigPath != "" {
		args = append(args, "--mcp-config", opts.MCPConfigPath)
	}
	if opts.MemoryDir != "" {
		args = append(args, "--add-dir", opts.MemoryDir)
	}
	// "--" terminates flag parsing so prompts starting with "-" aren't
	// mistaken for CLI options.
	args = append(args, "--", prompt)
	return args
}
