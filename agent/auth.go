package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty/v2"

	"tclaw/channel"
)

// isExpectedPTYError returns true for I/O errors that are normal when a pty
// child process exits (EIO on the master fd).
func isExpectedPTYError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.EIO {
		return true
	}
	return false
}

// authState tracks the multi-step authentication flow.
type authState int

const (
	authNone          authState = iota // no auth flow in progress
	authChoosing                       // waiting for user to pick oauth or api key
	authAPIKeyEntry                    // waiting for user to paste an api key
	authOAuthActive                    // `claude setup-token` is running in the background
	authDeployConfirm                  // asking user whether to deploy setup token to prod
)

// apiKeyPrefix is the expected prefix for Anthropic API keys.
const apiKeyPrefix = "sk-ant-"

// setupTokenTimeout limits how long we wait for `claude setup-token` to complete.
const setupTokenTimeout = 5 * time.Minute

// flyAppName is the Fly.io application name used for credential deployment.
const flyAppName = "tclaw"

// bold wraps text in bold markup appropriate for the channel.
func bold(m channel.Markup, s string) string {
	if m == channel.MarkupHTML {
		return "<b>" + s + "</b>"
	}
	return "**" + s + "**"
}

// code wraps text in inline code markup appropriate for the channel.
func code(m channel.Markup, s string) string {
	if m == channel.MarkupHTML {
		return "<code>" + s + "</code>"
	}
	return "`" + s + "`"
}

// authPrompt builds the auth choice message for the given channel markup.
func authPrompt(m channel.Markup) string {
	return "🔐 " + bold(m, "Authentication required") + "\n\n" +
		"Choose how to authenticate:\n" +
		bold(m, "1") + " — OAuth login (opens browser, local only)\n" +
		bold(m, "2") + " — API key (paste an Anthropic API key)\n" +
		bold(m, "3") + " — Cancel"
}

// apiKeyPrompt builds the API key entry prompt for the given channel markup.
func apiKeyPrompt(m channel.Markup) string {
	return "🔑 Paste your Anthropic API key (starts with " + code(m, "sk-ant-") + "):"
}

// oauthResult carries the outcome of an async `claude setup-token` goroutine
// back to the main event loop.
type oauthResult struct {
	setupToken   string // non-empty on success
	loginMessage string // success or failure message for the user
}

// pendingAuth tracks per-channel auth flow state so channels don't
// interfere with each other's auth flows.
type pendingAuth struct {
	state       authState
	originalMsg channel.TaggedMessage // message to retry after auth

	// OAuth flow: cancel function and result channel.
	oauthCancel context.CancelFunc
	oauthDone   chan oauthResult // receives exactly one result when OAuth completes

	// Stored after OAuth success, used for the deploy step.
	setupToken string
}

// cleanup cancels any running OAuth process.
func (p *pendingAuth) cleanup() {
	if p.oauthCancel != nil {
		p.oauthCancel()
		p.oauthCancel = nil
	}
}

// authStatus is the JSON output of `claude auth status --json`.
type authStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	APIProvider      string `json:"apiProvider"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

// startSetupToken launches `claude setup-token` in a background goroutine.
// The CLI opens the user's browser for OAuth and outputs a long-lived setup
// token to stdout. Sends exactly one result to flow.oauthDone when finished.
// All channel I/O is left to the caller (main event loop).
func startSetupToken(ctx context.Context, opts Options, flow *pendingAuth, channelID channel.ChannelID, notify chan<- channel.ChannelID) {
	tokenCtx, cancel := context.WithTimeout(ctx, setupTokenTimeout)
	flow.oauthCancel = cancel
	flow.oauthDone = make(chan oauthResult, 1)
	flow.state = authOAuthActive

	go func() {
		defer cancel()
		defer func() { notify <- channelID }()

		cmd := exec.CommandContext(tokenCtx, "claude", "setup-token")
		cmd.Env = buildEnv(opts)

		// Use a pty so the CLI sees a real TTY on all fds and opens the
		// browser for interactive OAuth. A plain bytes.Buffer on stdout
		// causes the CLI to detect piped output and skip the browser flow.
		// pty.Start overrides stdin/stdout/stderr to go through the pty.
		slog.Info("starting claude setup-token", "channel", channelID)
		// Use a wide terminal so the token (which can be ~120 chars) doesn't
		// get line-wrapped by the pty, which would break extraction.
		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 512})
		if err != nil {
			slog.Error("failed to start claude setup-token with pty", "err", err)
			flow.oauthDone <- oauthResult{loginMessage: "Setup token generation failed to start. Check server logs."}
			return
		}
		defer ptmx.Close()

		// Read all output from the pty master. The child's stdout and
		// stderr are both multiplexed through the pty.
		var stdout bytes.Buffer
		if _, err := io.Copy(&stdout, ptmx); err != nil {
			// EIO is expected when the child exits and the pty closes.
			if !isExpectedPTYError(err) {
				slog.Warn("error reading pty output", "err", err)
			}
		}

		if err := cmd.Wait(); err != nil {
			if tokenCtx.Err() != nil {
				flow.oauthDone <- oauthResult{loginMessage: "Setup token generation was cancelled."}
				return
			}
			slog.Error("claude setup-token failed", "err", err, "stdout_len", stdout.Len())
			flow.oauthDone <- oauthResult{loginMessage: "Setup token generation failed. Check server logs."}
			return
		}

		slog.Info("claude setup-token finished", "stdout_len", stdout.Len())

		token := extractSetupToken(stdout.String())
		if token == "" {
			slog.Error("claude setup-token: no token found in output", "stdout_len", stdout.Len())
			flow.oauthDone <- oauthResult{loginMessage: "Setup token generation succeeded but no token found in output."}
			return
		}

		slog.Info("claude setup-token completed", "token_len", len(token))

		flow.oauthDone <- oauthResult{
			setupToken:   token,
			loginMessage: "✅ Setup token generated.",
		}
	}()
}

// deployTimeout limits how long we wait for `fly secrets set` to complete.
const deployTimeout = 30 * time.Second

// deploySetupToken pushes the user's setup token to Fly.io as a per-user
// secret so headless production agents can authenticate without a browser.
func deploySetupToken(ctx context.Context, userID string, setupToken string) error {
	deployCtx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()

	envName := SetupTokenEnvVarName(userID)

	cmd := exec.CommandContext(deployCtx, "fly", "secrets", "set", envName+"="+setupToken, "-a", flyAppName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if deployCtx.Err() != nil {
			return fmt.Errorf("fly secrets set timed out after %s", deployTimeout)
		}
		return fmt.Errorf("fly secrets set: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	slog.Info("deployed setup token to Fly", "user", userID, "env_var", envName)
	return nil
}

// sanitizeEnvSuffix uppercases a user ID and replaces non-alphanumeric chars
// with underscores, producing a safe environment variable suffix.
func sanitizeEnvSuffix(userID string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, userID))
}

// SetupTokenEnvVarName returns the environment variable name used to store
// a user's setup token as a Fly secret. On the subprocess side, it gets
// mapped to CLAUDE_CODE_OAUTH_TOKEN which the CLI reads natively.
func SetupTokenEnvVarName(userID string) string {
	return "CLAUDE_SETUP_TOKEN_" + sanitizeEnvSuffix(userID)
}

// GitHubTokenEnvVarName returns the environment variable name used to
// pre-provision a user's GitHub PAT as a Fly secret. At boot, the router
// seeds this into the encrypted secret store so dev tools find it without
// prompting.
func GitHubTokenEnvVarName(userID string) string {
	return "GITHUB_TOKEN_" + sanitizeEnvSuffix(userID)
}

// FlyTokenEnvVarName returns the environment variable name used to
// pre-provision a user's Fly.io API token as a Fly secret. At boot, the router
// seeds this into the encrypted secret store so the deploy tool finds it
// without prompting.
func FlyTokenEnvVarName(userID string) string {
	return "FLY_TOKEN_" + sanitizeEnvSuffix(userID)
}

// TfLAPIKeyEnvVarName returns the environment variable name used to
// pre-provision a user's TfL API key as a Fly secret. At boot, the router
// seeds this into the encrypted secret store so TfL tools find it without prompting.
func TfLAPIKeyEnvVarName(userID string) string {
	return "TFL_API_KEY_" + sanitizeEnvSuffix(userID)
}

// ResyAPIKeyEnvVarName returns the environment variable name used to
// pre-provision a user's Resy API key as a Fly secret. At boot, the router
// seeds this into the encrypted secret store so restaurant tools find it without prompting.
func ResyAPIKeyEnvVarName(userID string) string {
	return "RESY_API_KEY_" + sanitizeEnvSuffix(userID)
}

// ResyAuthTokenEnvVarName returns the environment variable name used to
// pre-provision a user's Resy auth token as a Fly secret.
func ResyAuthTokenEnvVarName(userID string) string {
	return "RESY_AUTH_TOKEN_" + sanitizeEnvSuffix(userID)
}

// EnableBankingAppIDEnvVarName returns the environment variable name used to
// pre-provision a user's Enable Banking application ID as a Fly secret.
func EnableBankingAppIDEnvVarName(userID string) string {
	return "ENABLEBANKING_APP_ID_" + sanitizeEnvSuffix(userID)
}

// EnableBankingPrivateKeyEnvVarName returns the environment variable name used to
// pre-provision a user's Enable Banking RSA private key as a Fly secret.
func EnableBankingPrivateKeyEnvVarName(userID string) string {
	return "ENABLEBANKING_PRIVATE_KEY_" + sanitizeEnvSuffix(userID)
}

// setupTokenPrefix is the expected prefix for setup tokens from `claude setup-token`.
const setupTokenPrefix = "sk-ant-oat01-"

// ansiEscape matches ANSI escape sequences (CSI sequences and simple escapes).
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*\x1b\\|\x1b[^[\]]`)

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// validTokenPattern matches the expected character set for setup tokens
// (alphanumeric, hyphens, underscores).
var validTokenPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validAPIKeyPattern matches the expected character set for Anthropic API keys.
var validAPIKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// extractSetupToken parses the setup token from `claude setup-token` output.
// The command outputs a banner, the token on its own line, and instructions.
// We find the line containing the expected token prefix and extract it.
// Output may contain ANSI escape codes from the pty, so we strip those first.
// Returns "" if the extracted token fails format validation.
func extractSetupToken(output string) string {
	clean := stripANSI(output)
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, setupTokenPrefix); idx >= 0 {
			// Extract from the prefix to the end of the token (no spaces in tokens).
			token := line[idx:]
			if sp := strings.IndexByte(token, ' '); sp >= 0 {
				token = token[:sp]
			}
			if !validSetupToken(token) {
				slog.Warn("extracted setup token failed validation", "token_len", len(token))
				return ""
			}
			return token
		}
	}
	return ""
}

// validSetupToken checks that a token has reasonable length and expected characters.
func validSetupToken(token string) bool {
	return len(token) >= 50 && validTokenPattern.MatchString(token)
}

// handleAPIKeyEntry validates and persists an API key the user pasted.
// Returns true on success.
func handleAPIKeyEntry(ctx context.Context, opts Options, ch channel.Channel, key string) bool {
	key = strings.TrimSpace(key)

	if !strings.HasPrefix(key, apiKeyPrefix) || len(key) < 50 || !validAPIKeyPattern.MatchString(key) {
		m := ch.Markup()
		if _, err := ch.Send(ctx, "❌ Invalid key — must start with "+code(m, "sk-ant-")+" and be a valid length. Try again or type "+bold(m, "stop")+" to cancel."); err != nil {
			slog.Error("failed to send validation error", "err", err)
		}
		return false
	}

	if err := persistAPIKey(ctx, opts, key); err != nil {
		slog.Error("failed to persist api key", "err", err)
		if _, sendErr := ch.Send(ctx, "❌ Failed to save API key. Check server logs."); sendErr != nil {
			slog.Error("failed to send persist error", "err", sendErr)
		}
		return false
	}

	if _, err := ch.Send(ctx, "✅ API key saved."); err != nil {
		slog.Error("failed to send api key confirmation", "err", err)
	}
	return true
}

// handleAuthStatus checks and reports the current authentication status.
func handleAuthStatus(ctx context.Context, opts Options, ch channel.Channel) {
	status, err := checkAuthStatus(ctx, opts)
	if err != nil {
		if _, sendErr := ch.Send(ctx, fmt.Sprintf("❌ Failed to check auth status: %v", err)); sendErr != nil {
			slog.Error("failed to send auth status error", "err", sendErr)
		}
		return
	}

	if !status.LoggedIn {
		if _, sendErr := ch.Send(ctx, "🔒 Not logged in. Type "+bold(ch.Markup(), "login")+" to authenticate."); sendErr != nil {
			slog.Error("failed to send auth status", "err", sendErr)
		}
		return
	}

	m := ch.Markup()
	var msg string
	if status.Email != "" {
		// Full profile available (API key or first-party with profile)
		msg = fmt.Sprintf("🔓 Logged in as %s (%s, %s)\nAuth: %s | Provider: %s",
			bold(m, status.Email), status.OrgName, status.SubscriptionType,
			status.AuthMethod, status.APIProvider)
	} else {
		msg = fmt.Sprintf("🔓 Logged in\nAuth: %s | Provider: %s",
			status.AuthMethod, status.APIProvider)
	}
	if opts.APIKey != "" {
		msg += "\n⚠️ API key is configured — it takes precedence over OAuth."
	}
	if _, sendErr := ch.Send(ctx, msg); sendErr != nil {
		slog.Error("failed to send auth status", "err", sendErr)
	}
}

// checkAuthStatus runs `claude auth status --json` and parses the result.
func checkAuthStatus(ctx context.Context, opts Options) (*authStatus, error) {
	cmd := exec.CommandContext(ctx, "claude", "auth", "status", "--json")
	cmd.Env = buildEnv(opts)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude auth status: %w", err)
	}

	var status authStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("parse auth status: %w", err)
	}
	return &status, nil
}

// persistAPIKey stores the key in the encrypted secret store.
func persistAPIKey(ctx context.Context, opts Options, key string) error {
	if opts.SecretStore == nil {
		return fmt.Errorf("no secret store available")
	}
	return opts.SecretStore.Set(ctx, secretKeyAPIKey, key)
}

// loadPersistedAPIKey reads a previously stored API key from the encrypted
// secret store. Returns empty string if none exists or on error.
func loadPersistedAPIKey(ctx context.Context, opts Options) string {
	if opts.SecretStore == nil {
		return ""
	}
	key, err := opts.SecretStore.Get(ctx, secretKeyAPIKey)
	if err != nil {
		slog.Debug("no persisted API key found", "err", err)
		return ""
	}
	return key
}

// secretKeySetupToken is the key used to store the setup token in the secret store.
const secretKeySetupToken = "claude_setup_token"

// persistSetupToken stores the setup token in the encrypted secret store.
func persistSetupToken(ctx context.Context, opts Options, token string) error {
	if opts.SecretStore == nil {
		return fmt.Errorf("no secret store available")
	}
	return opts.SecretStore.Set(ctx, secretKeySetupToken, token)
}

// loadPersistedSetupToken reads a previously stored setup token from the
// encrypted secret store. Returns empty string if none exists or on error.
func loadPersistedSetupToken(ctx context.Context, opts Options) string {
	if opts.SecretStore == nil {
		return ""
	}
	token, err := opts.SecretStore.Get(ctx, secretKeySetupToken)
	if err != nil {
		slog.Debug("no persisted setup token found", "err", err)
		return ""
	}
	return token
}
