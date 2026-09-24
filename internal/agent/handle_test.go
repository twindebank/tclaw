package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
	"tclaw/internal/memorylayout"
)

// mockChannel records Send/Edit calls and can be configured to fail.
type mockChannel struct {
	sends     []string   // text of each Send call
	edits     []mockEdit // (id, text) of each Edit call
	nextID    int        // auto-increment for message IDs
	editError error      // if set, Edit returns this error
	info      channel.Info
}

type mockEdit struct {
	id   channel.MessageID
	text string
}

func (m *mockChannel) Info() channel.Info                       { return m.info }
func (m *mockChannel) Messages(_ context.Context) <-chan string { return nil }
func (m *mockChannel) Done(_ context.Context) error             { return nil }
func (m *mockChannel) SplitStatusMessages() bool                { return true }
func (m *mockChannel) Markup() channel.Markup                   { return channel.MarkupTelegram }
func (m *mockChannel) StatusWrap() channel.StatusWrap           { return channel.StatusWrap{} }

func (m *mockChannel) Send(_ context.Context, text string, _ channel.SendOpts) (channel.MessageID, error) {
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

func TestFriendlyErrorMessage(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		subtype claudecli.ResultSubtype
		want    string
	}{
		{
			name:    "spend cap says which setting stopped the turn",
			raw:     "",
			subtype: claudecli.ResultErrorMaxBudget,
			want:    "spend cap reached — the max_budget_usd cap stopped this turn. Send another message to carry on, or raise the cap",
		},
		{
			name:    "empty error falls back to subtype",
			raw:     "",
			subtype: claudecli.ResultErrorDuringExecution,
			want:    "claude ended the turn with an error (error_during_execution) but gave no details — check the logs",
		},
		{
			name:    "empty error and empty subtype still gives a message",
			raw:     "",
			subtype: "",
			want:    "claude ended the turn with an error but gave no details — check the logs",
		},
		{
			name: "session limit classified with reset time preserved",
			raw:  "You've hit your session limit · resets 1:50pm (UTC)",
			want: "usage limit reached — You've hit your session limit · resets 1:50pm (UTC)",
		},
		{
			name: "out of extra usage classified",
			raw:  "You're out of extra usage · resets 12:40pm (UTC)",
			want: "usage limit reached — You're out of extra usage · resets 12:40pm (UTC)",
		},
		{
			name: "unknown error passes through raw",
			raw:  "some novel failure",
			want: "claude error: some novel failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, friendlyErrorMessage(tt.raw, tt.subtype))
		})
	}
}

func TestWriteSplit(t *testing.T) {
	t.Run("proactive split status", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

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
	})

	t.Run("proactive split response", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

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
	})

	t.Run("edit failure recovery status", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

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
	})

	t.Run("edit failure recovery response", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

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
	})

	t.Run("response sealed after interleaved status", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		// 1. Status write creates msg 1.
		if err := tw.writeSplit(phaseStatus, "🤔 Thinking...\n"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ch.sends) != 1 {
			t.Fatalf("expected 1 send after status, got %d", len(ch.sends))
		}

		// 2. Response write creates msg 2 and seals status.
		if err := tw.writeSplit(phaseResponse, "Here is the answer"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ch.sends) != 2 {
			t.Fatalf("expected 2 sends after response, got %d", len(ch.sends))
		}
		if !tw.statusSealed {
			t.Fatal("expected statusSealed to be true after first response")
		}

		// 3. New status write creates msg 3 (statusSealed resets) and seals response.
		if err := tw.writeSplit(phaseStatus, "🔧 Using tool\n"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ch.sends) != 3 {
			t.Fatalf("expected 3 sends after second status, got %d", len(ch.sends))
		}
		if !tw.respSealed {
			t.Fatal("expected respSealed to be true after status sent below response")
		}

		// 4. Next response write must create msg 4 (not edit msg 2).
		if err := tw.writeSplit(phaseResponse, "More answer text"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ch.sends) != 4 {
			t.Fatalf("expected 4 sends (linear ordering), got %d", len(ch.sends))
		}
		if ch.sends[3] != "More answer text" {
			t.Fatalf("expected 4th message to be new response text, got %q", ch.sends[3])
		}

		// No response edits should have happened — each response was a fresh message.
		for _, edit := range ch.edits {
			if edit.id == "mm" {
				// "mm" is the ID of the 2nd send (response msg 2).
				t.Fatal("response msg 2 was edited after a status message appeared below it")
			}
		}
	})

	t.Run("truncates oversized status message", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		// Write a single chunk that exceeds the telegram truncation limit.
		oversized := strings.Repeat("x", telegramTruncateLen+500)
		if err := tw.writeSplit(phaseStatus, oversized); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ch.sends) != 1 {
			t.Fatalf("expected 1 send, got %d", len(ch.sends))
		}
		if len(ch.sends[0]) > telegramTruncateLen+20 {
			t.Fatalf("sent message too long: %d chars (limit %d)", len(ch.sends[0]), telegramTruncateLen+20)
		}
		if !strings.Contains(ch.sends[0], "[truncated]") {
			t.Fatal("expected truncation notice in sent message")
		}
	})

	t.Run("no split below threshold", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

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
	})
}

const testChannelID = channel.ChannelID("test")

func newTestTurnWriter(ch *mockChannel) *turnWriter {
	opts := Options{
		Channels: map[channel.ChannelID]channel.Channel{testChannelID: ch},
	}
	return &turnWriter{ch: ch, opts: opts, channelID: testChannelID, ctx: context.Background(), split: true}
}

func TestBuildEnv(t *testing.T) {
	t.Run("allowlist excludes dangerous vars", func(t *testing.T) {
		// Set dangerous env vars that should NOT leak to the subprocess.
		dangerous := map[string]string{
			"AWS_SECRET_ACCESS_KEY":          "secret",
			"GITHUB_TOKEN":                   "ghp_fake",
			"GH_TOKEN":                       "ghp_fake",
			"SSH_AUTH_SOCK":                  "/tmp/ssh-agent",
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

		env := buildEnv(Options{}, "")

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
	})

	t.Run("names the config directory so hooks and skills agree on it", func(t *testing.T) {
		// Hooks run under a shell that reads no profile. Left unset, a skill's
		// "$CLAUDE_CONFIG_DIR/feedback" resolves to the filesystem root, which
		// the sandbox then refuses.
		home := t.TempDir()

		env := buildEnv(Options{HomeDir: home}, "admin")

		require.Contains(t, env, memorylayout.EnvConfigDir+"="+filepath.Join(home, ".claude"))
	})

	t.Run("allows expected vars", func(t *testing.T) {
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

		env := buildEnv(Options{}, "")

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
	})

	t.Run("overrides always set", func(t *testing.T) {
		env := buildEnv(Options{
			HomeDir:    "/home/test",
			APIKey:     "sk-ant-test",
			SetupToken: "sk-ant-oat01-test",
		}, "")

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
	})

	t.Run("sets NODE_OPTIONS when env var present", func(t *testing.T) {
		t.Setenv("NODE_MAX_HEAP_MB", "128")
		env := buildEnv(Options{}, "")
		envMap := make(map[string]string)
		for _, kv := range env {
			key, val, _ := strings.Cut(kv, "=")
			envMap[key] = val
		}
		if envMap["NODE_OPTIONS"] != "--max-old-space-size=128" {
			t.Errorf("expected NODE_OPTIONS=--max-old-space-size=128, got %q", envMap["NODE_OPTIONS"])
		}
	})

	t.Run("tells the hooks which memory dir and channel the turn is on", func(t *testing.T) {
		env := buildEnv(Options{MemoryDir: "/data/alice/memory"}, "email")
		envMap := make(map[string]string)
		for _, kv := range env {
			key, val, _ := strings.Cut(kv, "=")
			envMap[key] = val
		}
		if envMap[memorylayout.EnvMemoryDir] != "/data/alice/memory" {
			t.Errorf("memory dir not passed to hooks, got %q", envMap[memorylayout.EnvMemoryDir])
		}
		if envMap[memorylayout.EnvChannel] != "email" {
			t.Errorf("channel not passed to hooks, got %q", envMap[memorylayout.EnvChannel])
		}
	})

	t.Run("omits NODE_OPTIONS when env var absent", func(t *testing.T) {
		env := buildEnv(Options{}, "")
		for _, kv := range env {
			key, _, _ := strings.Cut(kv, "=")
			if key == "NODE_OPTIONS" {
				t.Error("NODE_OPTIONS should not be set when NODE_MAX_HEAP_MB is absent")
			}
		}
	})
}

func TestSandboxPaths(t *testing.T) {
	t.Run("includes Go toolchain for dev sessions", func(t *testing.T) {
		var found bool
		for _, p := range systemReadOnlyPaths {
			if p == "/usr/local/go" {
				found = true
				break
			}
		}
		require.True(t, found, "systemReadOnlyPaths must include /usr/local/go for dev sessions")
	})
}

func TestStreamResponse(t *testing.T) {
	t.Run("streams text into the response as it arrives and never writes it twice", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		runStream(t, tw,
			streamLine(`{"type":"message_start"}`),
			streamLine(`{"type":"content_block_start","content_block":{"type":"text","text":""}}`),
			streamLine(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}`),
			streamLine(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}`),
			`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"text","text":"Hello"}]}}`,
			streamLine(`{"type":"content_block_stop"}`),
			streamLine(`{"type":"message_stop"}`),
		)

		require.Equal(t, []string{"Hel"}, ch.sends, "the first delta should open the response message")
		require.Equal(t, "Hello", ch.edits[len(ch.edits)-1].text, "the whole-block copy must not be appended again")
	})

	t.Run("shows a tool call with the input that streamed in after it started", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		runStream(t, tw,
			streamLine(`{"type":"message_start"}`),
			streamLine(`{"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_01A","name":"Bash","input":{}}}`),
			streamLine(`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`),
			streamLine(`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"\"echo hi\"}"}}`),
			`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"tool_use","id":"toolu_01A","name":"Bash","input":{"command":"echo hi"}}]}}`,
			streamLine(`{"type":"content_block_stop"}`),
			streamLine(`{"type":"message_stop"}`),
		)

		require.Len(t, ch.sends, 1)
		require.Equal(t, 1, strings.Count(ch.sends[0], "Bash"), "the tool line should be written once")
		require.Contains(t, ch.sends[0], "command=echo hi", "the tool line should carry the streamed input")
	})

	t.Run("keeps a subagent's text out of the reply when it arrives mid-block", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		// The interleaving a real Agent tool call produced: the subagent's whole
		// messages arrive while the main thread's block is still streaming.
		runStream(t, tw,
			streamLine(`{"type":"message_start"}`),
			streamLine(`{"type":"content_block_start","content_block":{"type":"text","text":""}}`),
			`{"type":"assistant","parent_tool_use_id":"toolu_01B","message":{"content":[{"type":"tool_use","id":"toolu_01C","name":"Read","input":{"file_path":"notes.md"}}]}}`,
			`{"type":"assistant","parent_tool_use_id":"toolu_01B","message":{"content":[{"type":"text","text":"subagent notes"}]}}`,
			streamLine(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Main answer"}}`),
			`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"text","text":"Main answer"}]}}`,
			streamLine(`{"type":"content_block_stop"}`),
			streamLine(`{"type":"message_stop"}`),
		)

		shown := strings.Join(finalTexts(ch), "\n")
		require.NotContains(t, shown, "subagent notes", "a subagent's text is not the reply")
		require.Contains(t, shown, "Read(file_path=notes.md)", "a subagent's tool call should show as progress")
		require.Equal(t, 1, strings.Count(shown, "Main answer"), "the main answer should be written once")
	})

	t.Run("shows an assistant message that was not streamed", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		// A command's reply, e.g. /compact on an empty session, comes only whole.
		runStream(t, tw,
			`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"text","text":"Not enough messages to compact."}]}}`,
		)

		require.Equal(t, []string{"Not enough messages to compact."}, ch.sends)
	})

	t.Run("shows an unstreamed message after a streamed one, as a separate paragraph", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		runStream(t, tw,
			streamLine(`{"type":"message_start"}`),
			streamLine(`{"type":"content_block_start","content_block":{"type":"text","text":""}}`),
			streamLine(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Streamed"}}`),
			`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"text","text":"Streamed"}]}}`,
			streamLine(`{"type":"content_block_stop"}`),
			streamLine(`{"type":"message_stop"}`),
			`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"text","text":"Not streamed"}]}}`,
		)

		require.Equal(t, []string{"Streamed\n\nNot streamed"}, finalTexts(ch))
	})

	t.Run("shows the CLI's error message even when a stream broke off", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		runStream(t, tw,
			streamLine(`{"type":"message_start"}`),
			`{"type":"assistant","parent_tool_use_id":null,"error":"server_error","message":{"content":[{"type":"text","text":"API Error: overloaded"}]}}`,
		)

		require.Equal(t, []string{"API Error: overloaded"}, finalTexts(ch))
	})

	t.Run("writes no tool result line for a user event that is not a tool result", func(t *testing.T) {
		ch := &mockChannel{}
		tw := newTestTurnWriter(ch)

		// What /compact feeds back: the summary and the command's output.
		runStream(t, tw,
			`{"type":"user","message":{"role":"user","content":"This session is being continued from a previous conversation."}}`,
			`{"type":"user","message":{"role":"user","content":"<local-command-stdout>Compacted </local-command-stdout>"}}`,
		)

		require.Empty(t, ch.sends)
	})
}

// --- helpers ---

// streamLine wraps a main-thread API event the way the CLI does.
func streamLine(event string) string {
	return `{"type":"stream_event","parent_tool_use_id":null,"event":` + event + `}`
}

// finalTexts returns what each message the channel was sent reads as after its last edit.
func finalTexts(ch *mockChannel) []string {
	texts := append([]string(nil), ch.sends...)
	for _, e := range ch.edits {
		// mockChannel's nth message ID is n copies of "m".
		texts[len(e.id)-1] = e.text
	}
	return texts
}

func runStream(t *testing.T, tw *turnWriter, lines ...string) {
	t.Helper()
	_, err := streamResponse(context.Background(), tw.opts, tw, strings.NewReader(strings.Join(lines, "\n")), nil, testChannelID, time.Now())
	require.NoError(t, err)
}

func TestTurnWriter_Drafts(t *testing.T) {
	t.Run("streams the reply as a draft and sends it once, in full, when status follows", func(t *testing.T) {
		ch := &draftChannel{}
		tw := newTurnWriter(context.Background(), Options{Channels: map[channel.ChannelID]channel.Channel{testChannelID: ch}}, testChannelID, ch)

		require.NoError(t, tw.write(phaseResponse, "Hello"))
		tw.lastDraft = time.Time{} // the next update is not held back by the interval
		require.NoError(t, tw.write(phaseResponse, " world"))
		require.Empty(t, ch.sends, "nothing is a message while it is a draft")
		require.Equal(t, []string{"Hello", "Hello world"}, ch.drafts)

		require.NoError(t, tw.write(phaseStatus, "📊 stats\n"))

		require.Equal(t, []string{"Hello world", "📊 stats\n"}, ch.sends, "the reply arrives before the status below it")
	})

	t.Run("sends a reply still shown as a draft when the turn ends", func(t *testing.T) {
		ch := &draftChannel{}
		tw := newTurnWriter(context.Background(), Options{Channels: map[channel.ChannelID]channel.Channel{testChannelID: ch}}, testChannelID, ch)

		require.NoError(t, tw.write(phaseResponse, "Partial"))
		require.NoError(t, tw.finish())

		require.Equal(t, []string{"Partial"}, ch.sends)
	})

	t.Run("falls back to an ordinary message when the channel refuses a draft", func(t *testing.T) {
		ch := &draftChannel{draftErr: errors.New("drafts are not allowed")}
		tw := newTurnWriter(context.Background(), Options{Channels: map[channel.ChannelID]channel.Channel{testChannelID: ch}}, testChannelID, ch)

		require.NoError(t, tw.write(phaseResponse, "Hello"))
		require.NoError(t, tw.write(phaseResponse, " world"))
		require.NoError(t, tw.finish())

		require.Equal(t, []string{"Hello"}, ch.sends)
		require.Equal(t, "Hello world", ch.edits[len(ch.edits)-1].text)
	})

	t.Run("continues a reply over the channel's rich limit in a new message", func(t *testing.T) {
		ch := &draftChannel{}
		tw := newTurnWriter(context.Background(), Options{Channels: map[channel.ChannelID]channel.Channel{testChannelID: ch}}, testChannelID, ch)
		first := strings.Repeat("a", ch.MaxRichReplyLen()-10)

		require.NoError(t, tw.write(phaseResponse, first))
		require.NoError(t, tw.write(phaseResponse, strings.Repeat("b", 20)))
		require.NoError(t, tw.finish())

		require.Equal(t, []string{first, strings.Repeat("b", 20)}, ch.sends)
	})
}

// draftChannel is a split-status channel that shows drafts and takes long rich replies.
type draftChannel struct {
	mockChannel
	drafts   []string
	draftErr error
}

func (d *draftChannel) StreamDraft(_ context.Context, p channel.StreamDraftParams) error {
	if d.draftErr != nil {
		return d.draftErr
	}
	d.drafts = append(d.drafts, p.Text)
	return nil
}

func (d *draftChannel) MaxRichReplyLen() int { return 5000 }
