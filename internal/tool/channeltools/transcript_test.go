package channeltools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/channeltools"
)

func TestChannelTranscript(t *testing.T) {
	t.Run("reads session transcript", func(t *testing.T) {
		h, deps := setupTranscript(t)

		// Write a session record and a JSONL file.
		ctx := context.Background()
		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "sess-001"))
		writeJSONL(t, deps.projectDir, "sess-001", []string{
			`{"type":"user","message":{"role":"user","content":"hello"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]}}`,
			`{"type":"user","message":{"role":"user","content":"how are you"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I'm doing well."}]}}`,
		})

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
		})

		require.Equal(t, "test-channel", result.Channel)
		require.Equal(t, "session", result.Source)
		require.Equal(t, 4, result.MessageCount)
		require.Equal(t, 1, result.SessionCount)
		require.Len(t, result.Sessions, 1)
		require.Equal(t, "sess-001", result.Sessions[0].SessionID)
		require.Len(t, result.Sessions[0].Messages, 4)
		require.Equal(t, "user", result.Sessions[0].Messages[0].Role)
		require.Equal(t, "hello", result.Sessions[0].Messages[0].Text)
		require.Equal(t, "assistant", result.Sessions[0].Messages[1].Role)
		require.Equal(t, "Hi there!", result.Sessions[0].Messages[1].Text)
	})

	t.Run("spans multiple sessions", func(t *testing.T) {
		h, deps := setupTranscript(t)
		ctx := context.Background()

		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "sess-001"))
		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "sess-002"))

		writeJSONL(t, deps.projectDir, "sess-001", []string{
			`{"type":"user","message":{"role":"user","content":"first session"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply 1"}]}}`,
		})
		writeJSONL(t, deps.projectDir, "sess-002", []string{
			`{"type":"user","message":{"role":"user","content":"second session"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply 2"}]}}`,
		})

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
		})

		require.Equal(t, 2, result.SessionCount)
		require.Equal(t, 4, result.MessageCount)
		// Sessions ordered oldest-first.
		require.Equal(t, "sess-001", result.Sessions[0].SessionID)
		require.Equal(t, "sess-002", result.Sessions[1].SessionID)
	})

	t.Run("respects limit", func(t *testing.T) {
		h, deps := setupTranscript(t)
		ctx := context.Background()

		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "sess-001"))
		writeJSONL(t, deps.projectDir, "sess-001", []string{
			`{"type":"user","message":{"role":"user","content":"msg 1"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply 1"}]}}`,
			`{"type":"user","message":{"role":"user","content":"msg 2"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply 2"}]}}`,
			`{"type":"user","message":{"role":"user","content":"msg 3"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply 3"}]}}`,
		})

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
			"limit":        2,
		})

		// Should return only the last 2 messages.
		require.Equal(t, 2, result.MessageCount)
		require.Equal(t, "msg 3", result.Sessions[0].Messages[0].Text)
		require.Equal(t, "reply 3", result.Sessions[0].Messages[1].Text)
	})

	t.Run("filters out tool_use and thinking", func(t *testing.T) {
		h, deps := setupTranscript(t)
		ctx := context.Background()

		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "sess-001"))
		writeJSONL(t, deps.projectDir, "sess-001", []string{
			`{"type":"user","message":{"role":"user","content":"do something"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"let me think..."},{"type":"tool_use","id":"t1","name":"Bash","input":{}},{"type":"text","text":"Done!"}]}}`,
			// Tool result messages (user role with array content) should be skipped.
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		})

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
		})

		require.Equal(t, 2, result.MessageCount)
		require.Equal(t, "do something", result.Sessions[0].Messages[0].Text)
		require.Equal(t, "Done!", result.Sessions[0].Messages[1].Text)
	})

	t.Run("skips non-text events", func(t *testing.T) {
		h, deps := setupTranscript(t)
		ctx := context.Background()

		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "sess-001"))
		writeJSONL(t, deps.projectDir, "sess-001", []string{
			`{"type":"queue-operation","operation":"enqueue"}`,
			`{"type":"system","message":{"role":"system"}}`,
			`{"type":"user","message":{"role":"user","content":"hello"}}`,
			`{"type":"content_block_start","content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","delta":{"text":"hi"}}`,
			`{"type":"content_block_stop"}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
			`{"type":"result","result":{}}`,
		})

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
		})

		require.Equal(t, 2, result.MessageCount)
	})

	t.Run("returns empty for channel with no sessions", func(t *testing.T) {
		h, _ := setupTranscript(t)

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
		})

		require.Equal(t, 0, result.MessageCount)
		require.Equal(t, 0, result.SessionCount)
		require.Empty(t, result.Sessions)
	})

	t.Run("returns empty for missing JSONL file", func(t *testing.T) {
		h, deps := setupTranscript(t)
		ctx := context.Background()

		// Record exists but JSONL file does not.
		require.NoError(t, deps.sessionStore.SetCurrent(ctx, channel.SessionKey(deps.channelID), "nonexistent"))

		result := callTranscript(t, h, map[string]any{
			"channel_name": "test-channel",
		})

		require.Equal(t, 0, result.MessageCount)
	})

	t.Run("rejects unknown channel", func(t *testing.T) {
		h, _ := setupTranscript(t)

		err := callTranscriptExpectError(t, h, map[string]any{
			"channel_name": "nonexistent",
		})
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("rejects unknown source", func(t *testing.T) {
		h, _ := setupTranscript(t)

		err := callTranscriptExpectError(t, h, map[string]any{
			"channel_name": "test-channel",
			"source":       "invalid",
		})
		require.Contains(t, err.Error(), "unknown source")
	})

	t.Run("telegram source not available", func(t *testing.T) {
		h, _ := setupTranscript(t)

		err := callTranscriptExpectError(t, h, map[string]any{
			"channel_name": "test-channel",
			"source":       "telegram",
		})
		require.Contains(t, err.Error(), "telegram source not available")
	})

	t.Run("telegram source delegates to handler", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := store.NewFS(filepath.Join(tmpDir, "sessions"))
		require.NoError(t, err)
		sessionStore := channel.NewSessionStore(fs)
		projectDir := filepath.Join(tmpDir, "home", ".claude", "projects", "-memory")
		require.NoError(t, os.MkdirAll(projectDir, 0o755))

		testCh := &stubChannel{name: "test-channel"}
		chID := testCh.Info().ID
		channelsFunc := func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{chID: testCh}
		}

		called := false
		telegramHistory := func(_ context.Context, name string, limit int) (json.RawMessage, error) {
			called = true
			require.Equal(t, "test-channel", name)
			require.Equal(t, 10, limit)
			return json.Marshal(map[string]any{"channel": name, "source": "telegram", "messages": []any{}})
		}

		handler := mcp.NewHandler()
		channeltools.RegisterTranscriptTool(handler, channeltools.TranscriptDeps{
			SessionStore:    sessionStore,
			HomeDir:         filepath.Join(tmpDir, "home"),
			MemoryDir:       "/memory",
			Channels:        channelsFunc,
			TelegramHistory: telegramHistory,
		})

		argsJSON, err := json.Marshal(map[string]any{
			"channel_name": "test-channel",
			"source":       "telegram",
			"limit":        10,
		})
		require.NoError(t, err)
		_, err = handler.Call(context.Background(), "channel_transcript", argsJSON)
		require.NoError(t, err)
		require.True(t, called)
	})
}

// --- helpers ---

type transcriptTestDeps struct {
	sessionStore *channel.SessionStore
	projectDir   string
	channelID    channel.ChannelID
}

func setupTranscript(t *testing.T) (*mcp.Handler, transcriptTestDeps) {
	t.Helper()
	tmpDir := t.TempDir()

	sessDir := filepath.Join(tmpDir, "sessions")
	fs, err := store.NewFS(sessDir)
	require.NoError(t, err)
	sessionStore := channel.NewSessionStore(fs)

	memoryDir := "/memory"
	homeDir := filepath.Join(tmpDir, "home")
	projectDir := filepath.Join(homeDir, ".claude", "projects", projectDirName(memoryDir))
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	testCh := &stubChannel{name: "test-channel"}
	chID := testCh.Info().ID
	channelsFunc := func() map[channel.ChannelID]channel.Channel {
		return map[channel.ChannelID]channel.Channel{chID: testCh}
	}

	handler := mcp.NewHandler()
	channeltools.RegisterTranscriptTool(handler, channeltools.TranscriptDeps{
		SessionStore: sessionStore,
		HomeDir:      homeDir,
		MemoryDir:    memoryDir,
		Channels:     channelsFunc,
	})

	return handler, transcriptTestDeps{
		sessionStore: sessionStore,
		projectDir:   projectDir,
		channelID:    chID,
	}
}

func projectDirName(path string) string {
	return fmt.Sprintf("-%s", path[1:])
}

func writeJSONL(t *testing.T, dir, sessionID string, lines []string) {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

type transcriptResult struct {
	Channel      string `json:"channel"`
	Source       string `json:"source"`
	MessageCount int    `json:"message_count"`
	SessionCount int    `json:"session_count"`
	Sessions     []struct {
		SessionID string `json:"session_id"`
		StartedAt string `json:"started_at"`
		Messages  []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	} `json:"sessions"`
}

func callTranscript(t *testing.T, h *mcp.Handler, args map[string]any) transcriptResult {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	raw, err := h.Call(context.Background(), "channel_transcript", argsJSON)
	require.NoError(t, err)
	var result transcriptResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func callTranscriptExpectError(t *testing.T, h *mcp.Handler, args map[string]any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), "channel_transcript", argsJSON)
	require.Error(t, err)
	return err
}

// stubChannel is defined in send_test.go — reused here since we're in the same package.
