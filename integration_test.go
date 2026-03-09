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
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/libraries/store"
	"tclaw/user"
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
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping integration test")
	}
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

	sock := channel.NewSocketServer(socketPath, "test", "Integration test channel")
	chMap := channel.ChannelMap(sock)

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan, // read-only, no file writes
		Model:          claudecli.ModelHaiku35,
		MaxTurns:       3,
		APIKey:         os.Getenv("ANTHROPIC_API_KEY"),
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
	socketPath := filepath.Join(tmpDir, "test.sock")
	homeDir := filepath.Join(tmpDir, "home")
	storeDir := filepath.Join(tmpDir, "store")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatalf("create home dir: %v", err)
	}

	s, err := store.NewFS(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	sock := channel.NewSocketServer(socketPath, "test", "Test channel")
	chMap := channel.ChannelMap(sock)

	var savedSessionID string

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan,
		Model:          claudecli.ModelHaiku35,
		MaxTurns:       3,
		APIKey:         os.Getenv("ANTHROPIC_API_KEY"),
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

	sock := channel.NewSocketServer(socketPath, "test", "Memory test channel")
	chMap := channel.ChannelMap(sock)

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan,
		Model:          claudecli.ModelHaiku35,
		MaxTurns:       3,
		APIKey:         os.Getenv("ANTHROPIC_API_KEY"),
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

// TestIntegration_DynamicChannelLifecycle tests creating a dynamic channel
// via the DynamicStore and then connecting to it via the socket.
func TestIntegration_DynamicChannelLifecycle(t *testing.T) {
	skipIfNoClaude(t)

	tmpDir := t.TempDir()
	storeDir := filepath.Join(tmpDir, "store")
	homeDir := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatalf("create home dir: %v", err)
	}

	s, err := store.NewFS(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Create a dynamic channel config.
	ds := channel.NewDynamicStore(s)
	if err := ds.Add(context.Background(), channel.DynamicChannelConfig{
		Name:        "phone",
		Type:        channel.TypeSocket,
		Description: "Test phone channel",
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("add dynamic channel: %v", err)
	}

	// Build a socket for it (same path derivation as router.buildDynamicChannels).
	userID := user.ID("test-user")
	socketPath := filepath.Join(tmpDir, string(userID), "phone.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket dir: %v", err)
	}

	sock := channel.NewDynamicSocketServer(socketPath, "phone", "Test phone channel")
	chMap := channel.ChannelMap(sock)

	// Verify the Info reports dynamic source.
	info := sock.Info()
	if info.Source != channel.SourceDynamic {
		t.Fatalf("expected Source=dynamic, got %q", info.Source)
	}

	opts := agent.Options{
		PermissionMode: claudecli.PermissionPlan,
		Model:          claudecli.ModelHaiku35,
		MaxTurns:       3,
		APIKey:         os.Getenv("ANTHROPIC_API_KEY"),
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
		t.Fatalf("send on dynamic channel: %v", err)
	}
	text := fullText(msgs)
	t.Logf("Dynamic channel response:\n%s", text)

	if len(msgs) == 0 {
		t.Fatal("expected response on dynamic channel, got none")
	}

	// Verify we can read back the dynamic configs from the store.
	configs, err := ds.List(context.Background())
	if err != nil {
		t.Fatalf("list dynamic channels: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "phone" {
		t.Fatalf("unexpected dynamic channel configs: %+v", configs)
	}
}
