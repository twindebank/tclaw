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
	"tclaw/provider"
)

const (
	// How long a pending OAuth flow stays valid before it's cleaned up.
	pendingFlowTTL = 10 * time.Minute
)

// PendingOAuthFlow is the interface that all OAuth flows must implement
// so the callback server can dispatch without knowing concrete types.
// Both Complete and Fail must close the done channel before returning.
type PendingOAuthFlow interface {
	// Complete is called when the OAuth callback arrives with an authorization code.
	// Must close DoneChan before returning (even on error).
	Complete(ctx context.Context, code string, callbackURL string) error
	// Fail records an error and closes the done channel.
	Fail(err error)
	// DoneChan returns a channel that's closed when the flow finishes.
	DoneChan() <-chan struct{}
}

// PendingFlow tracks an in-progress OAuth authorization for built-in providers.
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

func (f *PendingFlow) Complete(ctx context.Context, code string, callbackURL string) error {
	creds, err := ExchangeCode(ctx, f.Provider.OAuth2, code, f.CallbackURL)
	if err != nil {
		return fmt.Errorf("code exchange failed: %w", err)
	}

	if err := f.Manager.SetCredentials(ctx, f.ConnID, creds); err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}

	if f.OnConnect != nil {
		f.OnConnect()
	}

	f.Result = fmt.Sprintf("Connection %s authorized successfully", f.ConnID)
	close(f.Done)
	return nil
}

func (f *PendingFlow) Fail(err error) {
	f.Err = err
	close(f.Done)
}

func (f *PendingFlow) DoneChan() <-chan struct{} {
	return f.Done
}

// CallbackServer handles OAuth redirect callbacks on localhost.
// A single server runs per tclaw process, shared across all users.
type CallbackServer struct {
	addr    string
	pending sync.Map // state string -> PendingOAuthFlow
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
	s.pending.Store(state, PendingOAuthFlow(flow))
	return state, nil
}

// RegisterFlow stores any PendingOAuthFlow implementation with a generated state token.
// Used by external flow types (e.g. remote MCP) that manage their own initialization.
func (s *CallbackServer) RegisterFlow(flow PendingOAuthFlow) (string, error) {
	state, err := generateState()
	if err != nil {
		return "", err
	}
	s.pending.Store(state, flow)
	return state, nil
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

	flow, ok := val.(PendingOAuthFlow)
	if !ok {
		http.Error(w, "unknown flow type", http.StatusInternalServerError)
		return
	}

	if errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		flow.Fail(fmt.Errorf("oauth error: %s — %s", errParam, errDesc))
		fmt.Fprintf(w, "<html><body><h2>❌ Authorization denied</h2><p>%s</p><p>You can close this tab.</p></body></html>", errDesc)
		return
	}

	if code == "" {
		flow.Fail(fmt.Errorf("missing authorization code"))
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	if err := flow.Complete(r.Context(), code, s.CallbackURL()); err != nil {
		slog.Error("oauth flow completion failed", "err", err)
		flow.Fail(err)
		fmt.Fprintf(w, "<html><body><h2>❌ Authorization failed</h2><p>%s</p><p>You can close this tab.</p></body></html>", err.Error())
		return
	}

	fmt.Fprintf(w, "<html><body><h2>✅ Authorized!</h2><p>You can close this tab and return to your chat.</p></body></html>")
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return hex.EncodeToString(b), nil
}
