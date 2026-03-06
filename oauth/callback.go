package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/provider"
)

const (
	// How long a pending OAuth flow stays valid before it's cleaned up.
	pendingFlowTTL = 10 * time.Minute
)

// PendingFlow tracks an in-progress OAuth authorization.
type PendingFlow struct {
	ConnID      connection.ConnectionID
	Provider    *provider.Provider
	Manager     *connection.Manager
	CallbackURL string

	// OnConnect is called after credentials are stored successfully.
	// The router uses this to register provider tools dynamically
	// so they're available in the current session without restart.
	OnConnect func()

	// Done is closed when the flow completes (success or failure).
	// Result and Err are set before closing.
	Done   chan struct{}
	Result string // human-readable status message
	Err    error

	createdAt time.Time
}

// CallbackServer handles OAuth redirect callbacks on localhost.
// A single server runs per tclaw process, shared across all users.
type CallbackServer struct {
	addr    string
	pending sync.Map // state string -> *PendingFlow
	srv     *http.Server
	ln      net.Listener
}

// NewCallbackServer creates a callback server but does not start it.
func NewCallbackServer(addr string) *CallbackServer {
	return &CallbackServer{addr: addr}
}

// Start begins listening for OAuth callbacks.
func (s *CallbackServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", s.handleCallback)

	s.srv = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("oauth callback server error", "err", err)
		}
	}()

	slog.Info("oauth callback server started", "addr", s.addr)
	return nil
}

// Addr returns the address the server is listening on.
func (s *CallbackServer) Addr() string {
	return s.addr
}

// Stop gracefully shuts down the callback server.
func (s *CallbackServer) Stop(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// CallbackURL returns the full callback URL for OAuth redirect_uri.
func (s *CallbackServer) CallbackURL() string {
	return fmt.Sprintf("http://%s/oauth/callback", s.addr)
}

// StartFlow registers a pending OAuth flow and returns the state token.
func (s *CallbackServer) StartFlow(flow *PendingFlow) (string, error) {
	state, err := generateState()
	if err != nil {
		return "", err
	}
	flow.Done = make(chan struct{})
	flow.CallbackURL = s.CallbackURL()
	s.pending.Store(state, flow)
	return state, nil
}

// PendingRemoteMCPFlow tracks an in-progress OAuth flow for a remote MCP server.
// Unlike PendingFlow (for built-in providers), this uses the MCP OAuth 2.1
// discovery chain with PKCE and dynamic client registration.
type PendingRemoteMCPFlow struct {
	Name          string
	MCPURL        string
	AuthMeta      *mcp.AuthMetadata
	ClientReg     *mcp.ClientRegistration
	Manager       *connection.Manager
	ConfigUpdater func(ctx context.Context) error
	CodeVerifier  string // set after StartRemoteMCPFlow

	// Done is closed when the flow completes.
	Done   chan struct{}
	Result string
	Err    error
}

// StartRemoteMCPFlow registers a pending remote MCP OAuth flow and returns the
// state token, PKCE code verifier, and the full authorization URL.
func (s *CallbackServer) StartRemoteMCPFlow(flow *PendingRemoteMCPFlow, callbackURL, mcpURL string) (state, codeVerifier, authURL string, err error) {
	state, err = generateState()
	if err != nil {
		return "", "", "", err
	}

	flow.Done = make(chan struct{})

	authURL, codeVerifier = mcp.BuildAuthURLWithPKCE(flow.AuthMeta, flow.ClientReg, state, callbackURL, mcpURL)
	flow.CodeVerifier = codeVerifier

	s.pending.Store(state, flow)
	return state, codeVerifier, authURL, nil
}

func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	errParam := r.URL.Query().Get("error")

	if state == "" {
		http.Error(w, "missing state parameter", http.StatusBadRequest)
		return
	}

	val, ok := s.pending.LoadAndDelete(state)
	if !ok {
		http.Error(w, "unknown or expired authorization state", http.StatusBadRequest)
		return
	}

	// Dispatch based on flow type.
	switch flow := val.(type) {
	case *PendingFlow:
		s.handleBuiltinCallback(w, r, flow, code, errParam)
	case *PendingRemoteMCPFlow:
		s.handleRemoteMCPCallback(w, r, flow, code, errParam)
	default:
		http.Error(w, "unknown flow type", http.StatusInternalServerError)
	}
}

// handleBuiltinCallback handles OAuth callbacks for built-in provider connections.
func (s *CallbackServer) handleBuiltinCallback(w http.ResponseWriter, r *http.Request, flow *PendingFlow, code, errParam string) {
	if errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		flow.Err = fmt.Errorf("oauth error: %s — %s", errParam, errDesc)
		flow.Result = fmt.Sprintf("Authorization denied: %s", errDesc)
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>❌ Authorization denied</h2><p>%s</p><p>You can close this tab.</p></body></html>", errDesc)
		return
	}

	if code == "" {
		flow.Err = fmt.Errorf("missing authorization code")
		flow.Result = "Authorization failed: no code received"
		close(flow.Done)
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	slog.Info("oauth callback received", "connection", flow.ConnID)

	creds, err := ExchangeCode(r.Context(), flow.Provider.OAuth2, code, flow.CallbackURL)
	if err != nil {
		slog.Error("oauth code exchange failed", "connection", flow.ConnID, "err", err)
		flow.Err = fmt.Errorf("code exchange failed: %w", err)
		flow.Result = "Authorization failed: could not exchange code for tokens"
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>❌ Authorization failed</h2><p>Could not exchange code for tokens. Check the logs.</p><p>You can close this tab.</p></body></html>")
		return
	}

	if err := flow.Manager.SetCredentials(r.Context(), flow.ConnID, creds); err != nil {
		slog.Error("failed to store credentials", "connection", flow.ConnID, "err", err)
		flow.Err = fmt.Errorf("store credentials: %w", err)
		flow.Result = "Authorization succeeded but failed to store credentials"
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>⚠️ Partially failed</h2><p>Got tokens but failed to store them. Check the logs.</p><p>You can close this tab.</p></body></html>")
		return
	}

	if flow.OnConnect != nil {
		flow.OnConnect()
	}

	slog.Info("oauth flow completed", "connection", flow.ConnID)
	flow.Result = fmt.Sprintf("Connection %s authorized successfully", flow.ConnID)
	close(flow.Done)
	fmt.Fprintf(w, "<html><body><h2>✅ Authorized!</h2><p>Connection <strong>%s</strong> is now connected.</p><p>You can close this tab and return to your chat.</p></body></html>", flow.ConnID)
}

// handleRemoteMCPCallback handles OAuth callbacks for remote MCP servers.
func (s *CallbackServer) handleRemoteMCPCallback(w http.ResponseWriter, r *http.Request, flow *PendingRemoteMCPFlow, code, errParam string) {
	if errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		flow.Err = fmt.Errorf("oauth error: %s — %s", errParam, errDesc)
		flow.Result = fmt.Sprintf("Authorization denied: %s", errDesc)
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>❌ Authorization denied</h2><p>%s</p><p>You can close this tab.</p></body></html>", errDesc)
		return
	}

	if code == "" {
		flow.Err = fmt.Errorf("missing authorization code")
		flow.Result = "Authorization failed: no code received"
		close(flow.Done)
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	slog.Info("remote mcp oauth callback received", "name", flow.Name)

	// Exchange code for tokens using PKCE.
	creds, err := mcp.ExchangeCodeWithPKCE(
		r.Context(), flow.AuthMeta, flow.ClientReg,
		code, flow.CodeVerifier, s.CallbackURL(), flow.MCPURL,
	)
	if err != nil {
		slog.Error("remote mcp code exchange failed", "name", flow.Name, "err", err)
		flow.Err = fmt.Errorf("code exchange failed: %w", err)
		flow.Result = "Authorization failed: could not exchange code for tokens"
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>❌ Authorization failed</h2><p>Could not exchange code for tokens. Check the logs.</p><p>You can close this tab.</p></body></html>")
		return
	}

	// Update the stored auth with the new tokens.
	auth, err := flow.Manager.GetRemoteMCPAuth(r.Context(), flow.Name)
	if err != nil || auth == nil {
		slog.Error("failed to load remote mcp auth", "name", flow.Name, "err", err)
		flow.Err = fmt.Errorf("load auth for token storage: %w", err)
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>⚠️ Partially failed</h2><p>Got tokens but failed to store them.</p></body></html>")
		return
	}

	auth.AccessToken = creds.AccessToken
	auth.RefreshToken = creds.RefreshToken
	if creds.ExpiresIn > 0 {
		auth.TokenExpiry = time.Now().Add(time.Duration(creds.ExpiresIn) * time.Second)
	}

	if err := flow.Manager.SetRemoteMCPAuth(r.Context(), flow.Name, auth); err != nil {
		slog.Error("failed to store remote mcp tokens", "name", flow.Name, "err", err)
		flow.Err = fmt.Errorf("store tokens: %w", err)
		close(flow.Done)
		fmt.Fprintf(w, "<html><body><h2>⚠️ Partially failed</h2><p>Got tokens but failed to store them.</p></body></html>")
		return
	}

	// Regenerate the MCP config so the next turn picks up the new server.
	if flow.ConfigUpdater != nil {
		if err := flow.ConfigUpdater(r.Context()); err != nil {
			slog.Error("failed to update mcp config after remote auth", "err", err)
		}
	}

	slog.Info("remote mcp oauth flow completed", "name", flow.Name)
	flow.Result = fmt.Sprintf("Remote MCP %q authorized successfully", flow.Name)
	close(flow.Done)
	fmt.Fprintf(w, "<html><body><h2>✅ Authorized!</h2><p>Remote MCP <strong>%s</strong> is now connected.</p><p>You can close this tab and return to your chat.</p></body></html>", flow.Name)
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return hex.EncodeToString(b), nil
}
