package channeltools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tclaw/internal/channel"
	"tclaw/internal/mcp"
)

const ToolChannelTranscript = "channel_transcript"

// TranscriptDeps holds dependencies for the channel_transcript tool.
type TranscriptDeps struct {
	SessionStore *channel.SessionStore
	HomeDir      string
	MemoryDir    string
	Channels     func() map[channel.ChannelID]channel.Channel

	// TelegramHistory reads Telegram message history for a channel. Nil if
	// the Telegram Client API is not available.
	TelegramHistory func(ctx context.Context, channelName string, limit int) (json.RawMessage, error)
}

// RegisterTranscriptTool adds the channel_transcript tool to the MCP handler.
func RegisterTranscriptTool(handler *mcp.Handler, deps TranscriptDeps) {
	handler.Register(channelTranscriptDef(), channelTranscriptHandler(deps))
}

func channelTranscriptDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelTranscript,
		Description: "Read the conversation history of another channel. Returns messages grouped by session " +
			"with session boundaries. Use source \"session\" (default) for the full agent view (user messages " +
			"and assistant text responses), or \"telegram\" for the user-facing Telegram messages.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "The name of the channel to read history from."
				},
				"source": {
					"type": "string",
					"enum": ["session", "telegram"],
					"description": "Where to read history from. 'session' reads Claude Code transcripts (default). 'telegram' reads Telegram chat history via the Client API.",
					"default": "session"
				},
				"limit": {
					"type": "integer",
					"description": "Maximum number of messages to return (across all sessions). Most recent messages are returned first. Default 20, max 100.",
					"default": 20
				}
			},
			"required": ["channel_name"]
		}`),
	}
}

type transcriptParams struct {
	ChannelName string `json:"channel_name"`
	Source      string `json:"source"`
	Limit       int    `json:"limit"`
}

func channelTranscriptHandler(deps TranscriptDeps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p transcriptParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}

		if p.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required")
		}
		if p.Source == "" {
			p.Source = "session"
		}
		if p.Limit <= 0 {
			p.Limit = 20
		}
		if p.Limit > 100 {
			p.Limit = 100
		}

		switch p.Source {
		case "session":
			return handleSessionTranscript(ctx, deps, p)
		case "telegram":
			return handleTelegramTranscript(ctx, deps, p)
		default:
			return nil, fmt.Errorf("unknown source %q, must be \"session\" or \"telegram\"", p.Source)
		}
	}
}

// sessionTranscriptResult is the JSON response for source: "session".
type sessionTranscriptResult struct {
	Channel      string            `json:"channel"`
	Source       string            `json:"source"`
	MessageCount int               `json:"message_count"`
	SessionCount int               `json:"session_count"`
	Sessions     []sessionMessages `json:"sessions"`
}

type sessionMessages struct {
	SessionID string              `json:"session_id"`
	StartedAt string              `json:"started_at"`
	Messages  []transcriptMessage `json:"messages"`
}

type transcriptMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func handleSessionTranscript(ctx context.Context, deps TranscriptDeps, p transcriptParams) (json.RawMessage, error) {
	// Validate channel exists.
	if !channelExists(deps.Channels, p.ChannelName) {
		return nil, fmt.Errorf("channel %q not found", p.ChannelName)
	}

	// Resolve channel name to channel ID to look up sessions.
	chID := resolveChannelID(deps.Channels, p.ChannelName)
	if chID == "" {
		return nil, fmt.Errorf("channel %q not found in active channels", p.ChannelName)
	}

	channelKey := channel.SessionKey(chID)
	records, err := deps.SessionStore.List(ctx, channelKey)
	if err != nil {
		return nil, fmt.Errorf("list sessions for %q: %w", p.ChannelName, err)
	}

	if len(records) == 0 {
		return json.Marshal(sessionTranscriptResult{
			Channel:  p.ChannelName,
			Source:   "session",
			Sessions: []sessionMessages{},
		})
	}

	projectDir := projectDirName(deps.MemoryDir)

	// Read sessions in reverse (most recent first) until we hit the limit.
	remaining := p.Limit
	var allSessions []sessionMessages

	for i := len(records) - 1; i >= 0 && remaining > 0; i-- {
		rec := records[i]
		jsonlPath := filepath.Join(deps.HomeDir, ".claude", "projects", projectDir, rec.SessionID+".jsonl")

		messages, readErr := readSessionMessages(jsonlPath, remaining)
		if readErr != nil {
			// JSONL file may not exist (e.g. session was very short or file was cleaned up).
			continue
		}
		if len(messages) == 0 {
			continue
		}

		allSessions = append([]sessionMessages{{
			SessionID: rec.SessionID,
			StartedAt: rec.StartedAt.Format("2006-01-02T15:04:05Z"),
			Messages:  messages,
		}}, allSessions...)

		remaining -= len(messages)
	}

	totalMessages := 0
	for _, s := range allSessions {
		totalMessages += len(s.Messages)
	}

	return json.Marshal(sessionTranscriptResult{
		Channel:      p.ChannelName,
		Source:       "session",
		MessageCount: totalMessages,
		SessionCount: len(allSessions),
		Sessions:     allSessions,
	})
}

func handleTelegramTranscript(ctx context.Context, deps TranscriptDeps, p transcriptParams) (json.RawMessage, error) {
	if deps.TelegramHistory == nil {
		return nil, fmt.Errorf("telegram source not available — the Telegram Client API is not configured. Use source: \"session\" instead")
	}
	return deps.TelegramHistory(ctx, p.ChannelName, p.Limit)
}

// readSessionMessages reads the JSONL file and extracts user/assistant text
// messages, returning at most limit messages from the end of the file.
func readSessionMessages(path string, limit int) ([]transcriptMessage, error) {
	lines, err := readTailLines(path, 2000)
	if err != nil {
		return nil, err
	}
	return parseTranscriptMessages(lines, limit), nil
}

// parseTranscriptMessages filters JSONL lines into user/assistant text messages.
func parseTranscriptMessages(lines [][]byte, limit int) []transcriptMessage {
	var messages []transcriptMessage

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "user":
			text := extractUserText(entry.Message.Content)
			if text != "" {
				messages = append(messages, transcriptMessage{Role: "user", Text: text})
			}
		case "assistant":
			text := extractAssistantText(entry.Message.Content)
			if text != "" {
				messages = append(messages, transcriptMessage{Role: "assistant", Text: text})
			}
		}
	}

	// Return only the last `limit` messages.
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages
}

// extractUserText gets the text content from a user message. User messages
// can be a plain string or an array of content blocks (tool results).
// We only extract plain text messages, not tool results.
func extractUserText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	// Try plain string first.
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return text
	}

	// Array of content blocks — skip tool results, only extract text.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// extractAssistantText gets only text content blocks from an assistant message,
// skipping tool_use, thinking, and other block types.
func extractAssistantText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}

	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// projectDirName converts an absolute path to the Claude CLI's project
// directory name format. The CLI replaces path separators with dashes.
// e.g. "/private/tmp/tclaw/theo/memory" → "-private-tmp-tclaw-theo-memory"
func projectDirName(path string) string {
	return strings.ReplaceAll(path, "/", "-")
}

// readTailLines reads the last n lines from a file efficiently by seeking
// from the end. Returns all lines if the file has fewer than n lines.
func readTailLines(path string, n int) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	// For files up to ~2MB, just read the whole thing. For larger files,
	// seek from the end.
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript: %w", err)
	}

	const maxReadSize = 2 * 1024 * 1024 // 2MB
	if info.Size() > maxReadSize {
		// Seek to the last maxReadSize bytes.
		if _, err := f.Seek(-maxReadSize, io.SeekEnd); err != nil {
			return nil, fmt.Errorf("seek transcript: %w", err)
		}
	}

	scanner := bufio.NewScanner(f)
	// Allow large lines (some assistant responses with tool results are big).
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)

	var lines [][]byte
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}

	// Return only the last n lines.
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// channelExists checks if a channel with the given name exists.
func channelExists(channelsFunc func() map[channel.ChannelID]channel.Channel, name string) bool {
	for _, ch := range channelsFunc() {
		if ch.Info().Name == name {
			return true
		}
	}
	return false
}

// resolveChannelID finds the ChannelID for a channel name.
func resolveChannelID(channelsFunc func() map[channel.ChannelID]channel.Channel, name string) channel.ChannelID {
	for _, ch := range channelsFunc() {
		if ch.Info().Name == name {
			return ch.Info().ID
		}
	}
	return ""
}
