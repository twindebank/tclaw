package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"tclaw/channel"
)

// mockChannel records Send/Edit calls and can be configured to fail.
type mockChannel struct {
	sends     []string          // text of each Send call
	edits     []mockEdit        // (id, text) of each Edit call
	nextID    int               // auto-increment for message IDs
	editError error             // if set, Edit returns this error
	info      channel.Info
}

type mockEdit struct {
	id   channel.MessageID
	text string
}

func (m *mockChannel) Info() channel.Info                              { return m.info }
func (m *mockChannel) Messages(_ context.Context) <-chan string        { return nil }
func (m *mockChannel) Done(_ context.Context) error                    { return nil }
func (m *mockChannel) SplitStatusMessages() bool                       { return true }
func (m *mockChannel) Markup() channel.Markup                          { return channel.MarkupHTML }
func (m *mockChannel) ThinkingWrap() channel.ThinkingWrap              { return channel.ThinkingWrap{} }

func (m *mockChannel) Send(_ context.Context, text string) (channel.MessageID, error) {
	m.nextID++
	id := channel.MessageID(strings.Repeat("m", m.nextID))
	m.sends = append(m.sends, text)
	return id, nil
}

func (m *mockChannel) Edit(_ context.Context, id channel.MessageID, text string) error {
	if m.editError != nil {
		return m.editError
	}
	m.edits = append(m.edits, mockEdit{id: id, text: text})
	return nil
}

func TestWriteSplit_ProactiveSplitStatus(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// First write creates a new message.
	if err := tw.writeSplit(phaseStatus, "start\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ch.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(ch.sends))
	}

	// Write enough to exceed maxMessageLen.
	bigChunk := strings.Repeat("x", maxMessageLen)
	if err := tw.writeSplit(phaseStatus, bigChunk); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have sent a second message (the rotation).
	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after proactive split, got %d", len(ch.sends))
	}
	// The new message should contain only the big chunk, not the old content.
	if ch.sends[1] != bigChunk {
		t.Fatalf("expected new message to contain only the new chunk, got %d chars", len(ch.sends[1]))
	}
}

func TestWriteSplit_ProactiveSplitResponse(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	if err := tw.writeSplit(phaseResponse, "start\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ch.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(ch.sends))
	}

	bigChunk := strings.Repeat("y", maxMessageLen)
	if err := tw.writeSplit(phaseResponse, bigChunk); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after proactive split, got %d", len(ch.sends))
	}
	if ch.sends[1] != bigChunk {
		t.Fatalf("expected new message to contain only the new chunk, got %d chars", len(ch.sends[1]))
	}
}

func TestWriteSplit_EditFailureRecovery_Status(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// First write creates message.
	if err := tw.writeSplit(phaseStatus, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Make Edit fail.
	ch.editError = errors.New("MESSAGE_TOO_LONG")

	// Second write should recover: log error, send new message, not return error.
	if err := tw.writeSplit(phaseStatus, " world"); err != nil {
		t.Fatalf("status edit failure should not propagate, got: %v", err)
	}

	// Should have 2 sends: original + recovery.
	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after recovery, got %d", len(ch.sends))
	}
	// Recovery message should only contain the new text.
	if ch.sends[1] != " world" {
		t.Fatalf("expected recovery message to be ' world', got %q", ch.sends[1])
	}
}

func TestWriteSplit_EditFailureRecovery_Response(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// First write creates message.
	if err := tw.writeSplit(phaseResponse, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Make Edit fail.
	ch.editError = errors.New("MESSAGE_TOO_LONG")

	// Second write should recover with a new Send.
	if err := tw.writeSplit(phaseResponse, " world"); err != nil {
		t.Fatalf("response edit failure should recover, got: %v", err)
	}

	if len(ch.sends) != 2 {
		t.Fatalf("expected 2 sends after recovery, got %d", len(ch.sends))
	}
	if ch.sends[1] != " world" {
		t.Fatalf("expected recovery message to be ' world', got %q", ch.sends[1])
	}
}

func TestBuildEnv_AllowlistExcludesDangerousVars(t *testing.T) {
	// Set dangerous env vars that should NOT leak to the subprocess.
	dangerous := map[string]string{
		"AWS_SECRET_ACCESS_KEY":          "secret",
		"GITHUB_TOKEN":                   "ghp_fake",
		"GH_TOKEN":                       "ghp_fake",
		"SSH_AUTH_SOCK":                   "/tmp/ssh-agent",
		"GOOGLE_APPLICATION_CREDENTIALS": "/path/to/creds.json",
		"TCLAW_SECRET_KEY":               "masterkey",
		"CLAUDECODE":                     "1",
		"CLAUDE_CODE_ENTRYPOINT":         "/bin/claude",
		"DATABASE_URL":                   "postgres://secret",
		"OPENAI_API_KEY":                 "sk-openai",
	}
	for k, v := range dangerous {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	env := buildEnv(Options{})

	envMap := make(map[string]string)
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		envMap[key] = val
	}

	for k := range dangerous {
		if _, found := envMap[k]; found {
			t.Errorf("dangerous env var %q leaked to subprocess", k)
		}
	}
}

func TestBuildEnv_AllowsExpectedVars(t *testing.T) {
	allowed := map[string]string{
		"PATH":    "/usr/bin",
		"TERM":    "xterm-256color",
		"LANG":    "en_US.UTF-8",
		"LC_ALL":  "en_US.UTF-8",
		"TMPDIR":  "/tmp",
		"USER":    "testuser",
		"SHELL":   "/bin/zsh",
		"TZ":      "UTC",
		"EDITOR":  "vim",
		"VISUAL":  "code",
		"LOGNAME": "testuser",
	}
	for k, v := range allowed {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	env := buildEnv(Options{})

	envMap := make(map[string]string)
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		envMap[key] = val
	}

	for k, v := range allowed {
		if envMap[k] != v {
			t.Errorf("allowed env var %q=%q not found in subprocess env", k, v)
		}
	}
}

func TestBuildEnv_OverridesAlwaysSet(t *testing.T) {
	env := buildEnv(Options{
		HomeDir:    "/home/test",
		APIKey:     "sk-ant-test",
		SetupToken: "sk-ant-oat01-test",
	})

	envMap := make(map[string]string)
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		envMap[key] = val
	}

	if envMap["HOME"] != "/home/test" {
		t.Errorf("HOME override not set, got %q", envMap["HOME"])
	}
	if envMap["ANTHROPIC_API_KEY"] != "sk-ant-test" {
		t.Errorf("ANTHROPIC_API_KEY override not set, got %q", envMap["ANTHROPIC_API_KEY"])
	}
	if envMap["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-test" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN override not set, got %q", envMap["CLAUDE_CODE_OAUTH_TOKEN"])
	}
}

func TestWriteSplit_NoSplitBelowThreshold(t *testing.T) {
	ch := &mockChannel{}
	tw := &turnWriter{ch: ch, ctx: context.Background(), split: true}

	// Write many small chunks that stay under the limit.
	for i := 0; i < 100; i++ {
		if err := tw.writeSplit(phaseStatus, "ok\n"); err != nil {
			t.Fatalf("unexpected error on write %d: %v", i, err)
		}
	}

	// Only the first write should Send; all others should Edit.
	if len(ch.sends) != 1 {
		t.Fatalf("expected 1 send for small writes, got %d", len(ch.sends))
	}
	if len(ch.edits) != 99 {
		t.Fatalf("expected 99 edits, got %d", len(ch.edits))
	}
}
