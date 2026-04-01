package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// DefaultChannelKnowledgeTemplate is the initial content seeded into a new
// channel's knowledge directory. The placeholder {{name}} is replaced with
// the channel name before writing.
const DefaultChannelKnowledgeTemplate = `# {{name}} — Channel Knowledge

This file is loaded only when operating on the "{{name}}" channel.
Use it for channel-specific context, preferences, and notes.

Global memory (../../CLAUDE.md) is always loaded alongside this file.

## Notes
(none yet)
`

// seedChannelKnowledge ensures the channel's knowledge directory exists under
// memoryDir/channels/<channelName>/ and seeds a CLAUDE.md if missing. Returns
// the directory path, or empty string if memoryDir is empty.
func seedChannelKnowledge(memoryDir, channelName string) string {
	if memoryDir == "" || channelName == "" {
		return ""
	}

	dir := filepath.Join(memoryDir, "channels", channelName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("failed to create channel knowledge dir", "dir", dir, "err", err)
		return ""
	}

	mdPath := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		content := strings.ReplaceAll(DefaultChannelKnowledgeTemplate, "{{name}}", channelName)
		if writeErr := os.WriteFile(mdPath, []byte(content), 0o600); writeErr != nil {
			slog.Warn("failed to seed channel CLAUDE.md", "path", mdPath, "err", writeErr)
		}
	}

	return dir
}
