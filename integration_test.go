//go:build integration

// Integration tests that boot the full agent stack and exchange messages
// via unix sockets. Requires the `claude` CLI binary in PATH and a valid
// ANTHROPIC_API_KEY.
//
// Run with: go test -tags integration -timeout 5m -v ./...
// Or: go test -tags integration -timeout 5m -v -run TestIntegration ./
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tclaw/internal/agent"
	"tclaw/internal/channel"
	"tclaw/internal/channel/socketchannel"
	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/user"
)

// wireMsg matches the socket protocol (same as cmd/chat and channel/socket.go).
type testWireMsg struct {
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
	Text string `json:"text,omitempty"`
}

// socketClient is a minimal test client that talks to a SocketServer.
type socketClient struct {
	socketPath string
}

// send dials the socket, sends a message, and collects all response wire messages.
// The read side has a 2-minute deadline so tests don't hang if the agent dies.
func (c *socketClient) send(text string) ([]testWireMsg, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Set a generous read deadline — the agent may take a while to respond,
	// but if it dies we don't want to hang forever.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}

	fmt.Fprint(conn, text)
	conn.(*net.UnixConn).CloseWrite()

	var msgs []testWireMsg
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg testWireMsg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	// Deadline errors are expected when the agent closes the connection.
	if err := scanner.Err(); err != nil && !isTimeoutError(err) {
		return msgs, err
	}
	return msgs, nil
}

// isTimeoutError checks if an error is a net.Error timeout (from read deadline).
func isTimeoutError(err error) bool {
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	return false
}

// waitForSocket polls until the socket is accepting connections.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("socket %s not ready after %v", path, timeout)
}

// fullText concatenates all send/edit ops into the final message text.
// The last edit for each message ID wins (matching the real wire protocol semantics).
func fullText(msgs []testWireMsg) string {
	latest := make(map[string]string)
	var order []string
	for _, m := range msgs {
		if _, seen := latest[m.ID]; !seen {
			order = append(order, m.ID)
		}
		latest[m.ID] = m.Text
	}
	var parts []string
	for _, id := range order {
		parts = append(parts, latest[id])
	}
	return strings.Join(parts, "")
}

func skipIfNoClaude(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found in PATH — skipping integration test")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" && getSetupToken(t) == "" {
		t.Skip("neither ANTHROPIC_API_KEY nor setup token available — skipping integration test")
	}
}

// getSetupToken tries to read the Claude setup token from the macOS keychain.
// Returns empty string if unavailable. This allows integration tests to use
// OAuth credentials instead of an API key.
//
// The go-keyring library stores values as "go-keyring-base64:<base64>" so we
// need to strip the prefix and decode.
func getSetupToken(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("security", "find-generic-password",
		"-s", "tclaw/internal/theo", "-a", "claude_setup_token", "-w").Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))

	const prefix = "go-keyring-base64:"
	if strings.HasPrefix(raw, prefix) {
		decoded, err := base64.StdEncoding.DecodeString(raw[len(prefix):])
		if err != nil {
			t.Logf("failed to decode keychain token: %v", err)
			return ""
		}
		return string(decoded)
	}

	return raw
}

// agentCredentials returns the APIKey and SetupToken for integration tests,
// preferring the setup token from the keychain over an API key env var.
func agentCredentials(t *testing.T) (apiKey, setupToken string) {
	t.Helper()
	if token := getSetupToken(t); token != "" {
		return "", token
	}
	return os.Getenv("ANTHROPIC_API_KEY"), ""
}

// setupAgent creates a single-channel agent with a socket and returns
// a client to talk to it. The agent runs in a goroutine and is cancelled
// when the test ends.
func setupAgent(t *testing.T) *socketClient {
	t.Helper()
	skipIfNoClaude(t)

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	homeDir := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatalf("create home dir: %v", err)
	}

	sock := socketchannel.NewServer(socketPath, "test", "Integration test channel")
	chMap := channel.ChannelMap(sock)

	apiKey, setupToken := agentCredentials(t)
	// OAuth tokens (Pro/Teams) don't have access to Haiku — use Sonnet.
	model := claudecli.ModelHaiku35
	if setupToken != "" {
		model = claudecli.ModelSonnet46
	}

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan, // read-only, no file writes
		Model:          model,
		MaxTurns:       3,
		APIKey:         apiKey,
		SetupToken:     setupToken,
		HomeDir:        homeDir,
		Channels:       chMap,
		Sessions:       make(map[channel.ChannelID]string),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, opts)
	}()

	waitForSocket(t, socketPath, 5*time.Second)

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Log("agent did not stop within 10s")
		}
	})

	return &socketClient{socketPath: socketPath}
}

// TestIntegration_BasicResponse sends a simple message and verifies
// the agent responds with something non-empty.
func TestIntegration_BasicResponse(t *testing.T) {
	client := setupAgent(t)

	msgs, err := client.send("What is 2+2? Reply with ONLY the number, nothing else.")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	text := fullText(msgs)
	t.Logf("Response:\n%s", text)

	if len(msgs) == 0 {
		t.Fatal("expected at least one wire message, got none")
	}
	if !strings.Contains(text, "4") {
		t.Errorf("expected response to contain '4', got: %s", text)
	}
}

// TestIntegration_StopInterrupt sends a message, then sends "stop"
// to interrupt the turn.
func TestIntegration_StopInterrupt(t *testing.T) {
	client := setupAgent(t)

	// Send a slow-to-answer prompt in a goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		// This will get interrupted, so errors are expected.
		client.send("Write a very long detailed essay about the history of mathematics from ancient times to today. Make it at least 2000 words.")
	}()

	// Give the agent a moment to start processing, then interrupt.
	time.Sleep(3 * time.Second)

	// "stop" should not produce any agent output (it just cancels the turn).
	stopMsgs, err := client.send("stop")
	if err != nil {
		// Connection error is expected — the agent may close the socket
		// as it processes the cancellation.
		t.Logf("stop send (may error): %v", err)
	}
	_ = stopMsgs

	<-done
	t.Log("turn successfully interrupted")
}

// TestIntegration_SessionPersistence sends two messages and verifies
// the agent remembers context from the first in its response to the second.
func TestIntegration_SessionPersistence(t *testing.T) {
	skipIfNoClaude(t)

	tmpDir := t.TempDir()
	// Use a short path for the socket to avoid the 108-char Unix socket limit.
	// t.TempDir() paths can exceed this on macOS.
	sockDir, err := os.MkdirTemp("/tmp", "tg")
	if err != nil {
		t.Fatalf("create sock dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socketPath := filepath.Join(sockDir, "t.sock")
	homeDir := filepath.Join(tmpDir, "home")
	storeDir := filepath.Join(tmpDir, "store")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatalf("create home dir: %v", err)
	}

	s, err := store.NewFS(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	sock := socketchannel.NewServer(socketPath, "test", "Test channel")
	chMap := channel.ChannelMap(sock)

	var savedSessionID string

	spAPIKey, spSetupToken := agentCredentials(t)
	spModel := claudecli.ModelHaiku35
	if spSetupToken != "" {
		spModel = claudecli.ModelSonnet46
	}

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan,
		Model:          spModel,
		MaxTurns:       3,
		APIKey:         spAPIKey,
		SetupToken:     spSetupToken,
		HomeDir:        homeDir,
		Channels:       chMap,
		Sessions:       make(map[channel.ChannelID]string),
		OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
			savedSessionID = sessionID
			_ = s.Set(context.Background(), string(chID), []byte(sessionID))
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, opts)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Log("agent did not stop within 10s")
		}
	})

	waitForSocket(t, socketPath, 5*time.Second)
	client := &socketClient{socketPath: socketPath}

	// First message: establish a fact.
	msgs1, err := client.send("My favorite color is chartreuse. Just acknowledge this and say 'noted'.")
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	t.Logf("First response:\n%s", fullText(msgs1))

	if savedSessionID == "" {
		t.Fatal("expected session ID to be saved after first message")
	}

	// Second message: ask about the established fact.
	msgs2, err := client.send("What is my favorite color? Reply with ONLY the color name.")
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	text2 := fullText(msgs2)
	t.Logf("Second response:\n%s", text2)

	if !strings.Contains(strings.ToLower(text2), "chartreuse") {
		t.Errorf("expected agent to remember 'chartreuse', got: %s", text2)
	}
}

// TestIntegration_MemoryLoaded seeds a unique fact into memory/CLAUDE.md,
// sets up the symlink at home/.claude/CLAUDE.md, starts the agent with
// MemoryDir set, and verifies the agent can recall the fact — proving the
// full pipeline (memory dir → symlink → CLI auto-load → agent context).
func TestIntegration_MemoryLoaded(t *testing.T) {
	skipIfNoClaude(t)

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	homeDir := filepath.Join(tmpDir, "home")
	memoryDir := filepath.Join(tmpDir, "memory")
	claudeDir := filepath.Join(homeDir, ".claude")

	for _, dir := range []string{homeDir, memoryDir, claudeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
	}

	// Seed a unique fact into memory/CLAUDE.md.
	memoryContent := "# Memory\n\nThe user's secret codeword is \"flamingo7242\".\n"
	if err := os.WriteFile(filepath.Join(memoryDir, "CLAUDE.md"), []byte(memoryContent), 0o600); err != nil {
		t.Fatalf("write memory CLAUDE.md: %v", err)
	}

	// Create the symlink: home/.claude/CLAUDE.md → ../../memory/CLAUDE.md
	if err := os.Symlink(filepath.Join("..", "..", "memory", "CLAUDE.md"), filepath.Join(claudeDir, "CLAUDE.md")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	sock := socketchannel.NewServer(socketPath, "test", "Memory test channel")
	chMap := channel.ChannelMap(sock)

	mlAPIKey, mlSetupToken := agentCredentials(t)
	mlModel := claudecli.ModelHaiku35
	if mlSetupToken != "" {
		mlModel = claudecli.ModelSonnet46
	}

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan,
		Model:          mlModel,
		MaxTurns:       3,
		APIKey:         mlAPIKey,
		SetupToken:     mlSetupToken,
		HomeDir:        homeDir,
		MemoryDir:      memoryDir,
		Channels:       chMap,
		Sessions:       make(map[channel.ChannelID]string),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, opts)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Log("agent did not stop within 10s")
		}
	})

	waitForSocket(t, socketPath, 5*time.Second)
	client := &socketClient{socketPath: socketPath}

	msgs, err := client.send("What is my secret codeword? Reply with ONLY the codeword, nothing else.")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	text := fullText(msgs)
	t.Logf("Response:\n%s", text)

	if !strings.Contains(strings.ToLower(text), "flamingo7242") {
		t.Errorf("expected agent to recall 'flamingo7242' from memory, got: %s", text)
	}
}

// TestIntegration_ChannelLifecycle tests creating a channel via config
// and connecting to it via socket.
func TestIntegration_DynamicChannelLifecycle(t *testing.T) {
	skipIfNoClaude(t)

	// Use a short base path for sockets to avoid the 108-char Unix socket limit.
	tmpDir, err := os.MkdirTemp("/tmp", "tg")
	if err != nil {
		t.Fatalf("create tmp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	homeDir := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatalf("create home dir: %v", err)
	}

	// Build a socket channel directly (same as router.BuildChannels would).
	userID := user.ID("test-user")
	socketPath := filepath.Join(tmpDir, string(userID), "phone.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket dir: %v", err)
	}

	sock := socketchannel.NewServer(socketPath, "phone", "Test phone channel")
	chMap := channel.ChannelMap(sock)

	dcAPIKey, dcSetupToken := agentCredentials(t)
	dcModel := claudecli.ModelHaiku35
	if dcSetupToken != "" {
		dcModel = claudecli.ModelSonnet46
	}

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan,
		Model:          dcModel,
		MaxTurns:       3,
		APIKey:         dcAPIKey,
		SetupToken:     dcSetupToken,
		HomeDir:        homeDir,
		Channels:       chMap,
		Sessions:       make(map[channel.ChannelID]string),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, opts)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Log("agent did not stop within 10s")
		}
	})

	waitForSocket(t, socketPath, 5*time.Second)
	client := &socketClient{socketPath: socketPath}

	msgs, err := client.send("Say hello. Reply with only 'hello'.")
	if err != nil {
		t.Fatalf("send on agent-created channel: %v", err)
	}
	text := fullText(msgs)
	t.Logf("Agent-created channel response:\n%s", text)

	if len(msgs) == 0 {
		t.Fatal("expected response on agent-created channel, got none")
	}

	// Verify we can read back the channel configs from the store.
	configs, err := ds.List(context.Background())
	if err != nil {
		t.Fatalf("list channel configs: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "phone" {
		t.Fatalf("unexpected channel configs: %+v", configs)
	}
}

// TestIntegration_MCPToolGlobPermission verifies that MCP tools are allowed
// when using a glob pattern like "mcp__tclaw__test_*" in allowedTools with
// dontAsk permission mode. This reproduces a production issue where the CLI
// was blocking MCP tool calls despite glob pattern matching.
func TestIntegration_MCPToolGlobPermission(t *testing.T) {
	skipIfNoClaude(t)

	// Use a short temp path to stay within Unix socket's 108-char limit.
	tmpDir, err := os.MkdirTemp("/tmp", "tg")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	socketPath := filepath.Join(tmpDir, "t.sock")
	homeDir := filepath.Join(tmpDir, "home")
	stateDir := filepath.Join(tmpDir, "state")
	for _, dir := range []string{homeDir, stateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create dir: %v", err)
		}
	}

	// Start a minimal MCP server with a test tool.
	var toolCalled atomic.Bool
	handler := mcp.NewHandler()
	handler.Register(mcp.ToolDef{
		Name:        "test_ping",
		Description: "Returns pong. Use this tool when asked to ping.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		toolCalled.Store(true)
		return json.RawMessage(`"pong"`), nil
	})

	mcpServer := mcp.NewServer(handler)
	mcpAddr, err := mcpServer.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start MCP server: %v", err)
	}
	t.Cleanup(func() { mcpServer.Stop(context.Background()) })

	// Generate MCP config file with the server's bearer token.
	mcpConfigPath, err := mcp.GenerateConfigFile(stateDir, mcpAddr, mcpServer.Token(), nil)
	if err != nil {
		t.Fatalf("generate MCP config: %v", err)
	}

	sock := socketchannel.NewServer(socketPath, "test", "Integration test channel")
	chMap := channel.ChannelMap(sock)
	chID := sock.Info().ID

	apiKey, setupToken := agentCredentials(t)
	// OAuth tokens (Pro/Teams) don't have access to Haiku — use Sonnet.
	model := claudecli.ModelHaiku35
	if setupToken != "" {
		model = claudecli.ModelSonnet46
	}
	opts := agent.Options{
		PermissionMode: claudecli.PermissionDontAsk,
		Model:          model,
		MaxTurns:       5,
		APIKey:         apiKey,
		SetupToken:     setupToken,
		HomeDir:        homeDir,
		MCPConfigPath:  mcpConfigPath,
		MCPToolNames:   func() []string { return []string{"test_ping"} },
		Debug:          true,
		Channels:       chMap,
		Sessions:       make(map[channel.ChannelID]string),
		// Use a glob pattern to allow the MCP tool — this is what prod does.
		ChannelToolOverrides: map[channel.ChannelID]agent.ChannelToolPermissions{
			chID: {
				AllowedTools: []claudecli.Tool{
					"mcp__tclaw__test_*", // glob pattern — expanded by expandMCPGlobs
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, opts)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Log("agent did not stop within 10s")
		}
	})

	waitForSocket(t, socketPath, 5*time.Second)
	client := &socketClient{socketPath: socketPath}

	msgs, err := client.send("Call the test_ping tool and tell me what it returns. Do not ask for permission, just call it.")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	text := fullText(msgs)
	t.Logf("Response:\n%s", text)

	if !toolCalled.Load() {
		t.Error("MCP tool was never called — glob pattern in allowedTools may not be working")
	}

	textLower := strings.ToLower(text)
	if strings.Contains(textLower, "permission") || strings.Contains(textLower, "approval") || strings.Contains(textLower, "approve") {
		t.Errorf("agent asked for permission instead of calling the tool: %s", text)
	}

	if !strings.Contains(textLower, "pong") {
		t.Errorf("expected response to contain 'pong', got: %s", text)
	}
}
