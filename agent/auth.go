package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tclaw/channel"
)

// authState tracks the multi-step authentication flow.
type authState int

const (
	authNone          authState = iota // no auth flow in progress
	authChoosing                       // waiting for user to pick oauth or api key
	authAPIKeyEntry                    // waiting for user to paste an api key
	authOAuthActive                    // `claude auth login` is running in the background
	authDeployConfirm                  // asking user whether to deploy creds to prod
)

// apiKeyPrefix is the expected prefix for Anthropic API keys.
const apiKeyPrefix = "sk-ant-"

// oauthLoginTimeout limits how long we wait for `claude auth login` to complete.
const oauthLoginTimeout = 5 * time.Minute

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

// oauthResult carries the outcome of an async `claude auth login` goroutine
// back to the main event loop.
type oauthResult struct {
	credentialsJSON string // non-empty on success
	loginMessage    string // success or failure message for the user
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
	credentialsJSON string
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

// startOAuthLogin launches `claude auth login` in a background goroutine.
// The CLI opens the user's browser for OAuth. Sends exactly one result to
// flow.oauthDone when finished. All channel I/O is left to the caller
// (main event loop) — the goroutine does not send messages or call Done.
func startOAuthLogin(ctx context.Context, opts Options, flow *pendingAuth, channelID channel.ChannelID, notify chan<- channel.ChannelID) {
	oauthCtx, cancel := context.WithTimeout(ctx, oauthLoginTimeout)
	flow.oauthCancel = cancel
	flow.oauthDone = make(chan oauthResult, 1)
	flow.state = authOAuthActive

	go func() {
		defer cancel()
		defer func() { notify <- channelID }()

		// Temporarily hide the Keychains symlink so the CLI falls back to
		// writing .credentials.json instead of macOS Keychain. This ensures
		// credentials are per-user (each user has their own HOME) rather
		// than sharing a single Keychain entry across all users.
		keychainsPath := filepath.Join(opts.HomeDir, "Library", "Keychains")
		hiddenPath := keychainsPath + ".hidden"
		keychainHidden := false
		if err := os.Rename(keychainsPath, hiddenPath); err == nil {
			keychainHidden = true
		}
		restoreKeychain := func() {
			if keychainHidden {
				if err := os.Rename(hiddenPath, keychainsPath); err != nil {
					slog.Error("failed to restore Keychains symlink", "err", err)
				}
			}
		}
		defer restoreKeychain()

		cmd := exec.CommandContext(oauthCtx, "claude", "auth", "login")
		cmd.Env = buildEnv(opts)

		output, err := cmd.CombinedOutput()
		if err != nil {
			if oauthCtx.Err() != nil {
				flow.oauthDone <- oauthResult{loginMessage: "OAuth login was cancelled."}
				return
			}
			slog.Error("claude auth login failed", "err", err, "output", string(output))
			flow.oauthDone <- oauthResult{loginMessage: "OAuth login failed. Check server logs."}
			return
		}

		slog.Info("claude auth login completed", "output", string(output))

		// Restore Keychain immediately so subsequent CLI calls can use it.
		restoreKeychain()
		keychainHidden = false

		// The CLI output already confirms "Login successful." so we skip
		// `claude auth status --json` which can hang. Just read the
		// .credentials.json that the CLI wrote (Keychain was hidden during
		// login, forcing file-based storage in the per-user HOME).
		credPath := filepath.Join(opts.HomeDir, ".claude", ".credentials.json")
		credsData, readErr := os.ReadFile(credPath)
		if readErr != nil {
			slog.Error("failed to read credentials after OAuth", "err", readErr, "path", credPath)
			flow.oauthDone <- oauthResult{
				loginMessage: "Login succeeded but couldn't read credentials file for prod deployment.",
			}
			return
		}

		// Validate the credentials JSON has the expected structure.
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(credsData, &parsed); err != nil {
			slog.Error("credentials file is not valid JSON", "err", err)
			flow.oauthDone <- oauthResult{loginMessage: "Login succeeded but credentials file is malformed."}
			return
		}
		if _, ok := parsed["claudeAiOauth"]; !ok {
			slog.Error("credentials file missing claudeAiOauth key")
			flow.oauthDone <- oauthResult{loginMessage: "Login succeeded but credentials file is missing OAuth data."}
			return
		}

		flow.oauthDone <- oauthResult{
			credentialsJSON: string(credsData),
			loginMessage:    "✅ OAuth login successful.",
		}
	}()
}

// deployOAuthCredentials pushes the user's OAuth credentials to Fly.io as a
// per-user secret so headless production agents can use OAuth without a browser.
func deployOAuthCredentials(userID string, credentialsJSON string) error {
	envName := OAuthCredsEnvVarName(userID)
	arg := envName + "=" + credentialsJSON

	cmd := exec.Command("fly", "secrets", "set", arg, "-a", flyAppName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fly secrets set: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	slog.Info("deployed OAuth credentials to Fly", "user", userID, "env_var", envName)
	return nil
}

// OAuthCredsEnvVarName returns the environment variable name used to store
// a user's OAuth credentials as a Fly secret.
func OAuthCredsEnvVarName(userID string) string {
	sanitized := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, userID)
	return "CLAUDE_OAUTH_CREDS_" + strings.ToUpper(sanitized)
}

// handleAPIKeyEntry validates and persists an API key the user pasted.
// Returns true on success.
func handleAPIKeyEntry(ctx context.Context, opts Options, ch channel.Channel, key string) bool {
	key = strings.TrimSpace(key)

	if !strings.HasPrefix(key, apiKeyPrefix) {
		m := ch.Markup()
		if _, err := ch.Send(ctx, "❌ Invalid key — must start with "+code(m, "sk-ant-")+". Try again or type "+bold(m, "stop")+" to cancel."); err != nil {
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
	msg := fmt.Sprintf("🔓 Logged in as %s (%s, %s)\nAuth: %s | Provider: %s",
		bold(m, status.Email), status.OrgName, status.SubscriptionType,
		status.AuthMethod, status.APIProvider)
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

// ProvisionOAuthCredentials writes OAuth credentials JSON to the per-user
// HOME's .claude/.credentials.json. Used on startup to pre-provision auth
// from a deployed secret (e.g. Fly.io env var) so headless agents can use
// OAuth without an interactive browser flow.
// Does nothing if homeDir or credentialsJSON is empty.
// Always overwrites existing credentials — the deployed secret is the
// authoritative source in headless environments.
func ProvisionOAuthCredentials(homeDir string, credentialsJSON string) error {
	if homeDir == "" || credentialsJSON == "" {
		slog.Warn("skipping OAuth provisioning: empty homeDir or credentials", "homeDir_empty", homeDir == "", "creds_empty", credentialsJSON == "")
		return nil
	}

	// Minimal validation — must be valid JSON with the expected key.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(credentialsJSON), &parsed); err != nil {
		return fmt.Errorf("invalid credentials JSON: %w", err)
	}
	if _, ok := parsed["claudeAiOauth"]; !ok {
		return fmt.Errorf("credentials JSON missing claudeAiOauth key")
	}

	claudeDir := filepath.Join(homeDir, ".claude")
	credPath := filepath.Join(claudeDir, ".credentials.json")

	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return fmt.Errorf("create .claude dir: %w", err)
	}

	if err := os.WriteFile(credPath, []byte(credentialsJSON), 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	slog.Info("provisioned OAuth credentials", "path", credPath)
	return nil
}
