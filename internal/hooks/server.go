package hooks

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// hookPathPrefix is where each hook is served, followed by its name.
const hookPathPrefix = "/hooks/"

// Server answers the CLI's hook requests for one user, on loopback.
//
// It has no rate limit and no body cap below what the CLI sends: a request this server refused
// to read would reach the CLI as an error, and the CLI lets the tool call through on an error.
type Server struct {
	env   Env
	token string

	mu       sync.Mutex
	listener net.Listener
	srv      *http.Server
}

// NewServer creates a hook server for one user. It does not listen until Start.
func NewServer(env Env) *Server {
	return &Server{env: env, token: generateToken()}
}

// Token is the bearer token the CLI must present.
func (s *Server) Token() string {
	return s.token
}

// Start begins serving on addr, e.g. "127.0.0.1:0", and returns the base URL hooks are registered under.
func (s *Server) Start(addr string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return "", fmt.Errorf("hook server already started")
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+hookPathPrefix+"{name}", s.handleHook)
	s.listener = ln
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("hook server error", "err", err)
		}
	}()
	return "http://" + ln.Addr().String(), nil
}

// Stop shuts the server down.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// handleHook runs one hook and answers in the CLI's hook output format.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	spec, ok := specFor(name)
	if !ok {
		// A stale registration must not start refusing tool calls.
		slog.Warn("request for unknown hook", "hook", name)
		w.WriteHeader(http.StatusOK)
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		slog.Warn("hook request with a bad token", "hook", name)
		if spec.Event == eventPreToolUse {
			// Anything but a 2xx lets the call through, so a refusal has to be an answer.
			writeJSON(w, name, renderOutcome(spec.Event, Outcome{Refusal: "[" + name + "]: Refused: the hook request was not authenticated."}))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	input, err := io.ReadAll(r.Body)
	if err != nil {
		// Evaluate refuses for the rules gate when the input does not parse.
		slog.Warn("failed to read hook request", "hook", name, "err", err)
		input = nil
	}
	env := s.env
	env.Channel = r.Header.Get(headerChannel)
	writeJSON(w, name, renderOutcome(spec.Event, Evaluate(name, env, input)))
}

// renderOutcome builds the CLI's hook output for an outcome, or nil to say nothing.
func renderOutcome(event HookEvent, o Outcome) map[string]any {
	if o.Refusal != "" {
		return map[string]any{
			"hookSpecificOutput": map[string]string{
				"hookEventName":            string(event),
				"permissionDecision":       "deny",
				"permissionDecisionReason": o.Refusal,
			},
		}
	}
	if o.Context == "" && o.Notice == "" {
		return nil
	}
	body := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     string(event),
			"additionalContext": o.Context,
		},
	}
	if o.Notice != "" {
		// An empty one would still be a key the CLI has to decide to ignore.
		body["systemMessage"] = o.Notice
	}
	return body
}

// writeJSON answers 200 with body, or with no body when there is nothing to say.
func writeJSON(w http.ResponseWriter, hook string, body map[string]any) {
	if body == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		slog.Error("failed to encode hook output", "hook", hook, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(encoded); err != nil {
		slog.Warn("failed to write hook output", "hook", hook, "err", err)
	}
}

// generateToken returns a cryptographically random 32-byte hex string.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
