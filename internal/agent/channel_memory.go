package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// RulesDirName is the directory under the memory dir holding every rulebook, for
// every channel. Rulebooks live in one pool rather than per channel so a channel
// can reach one that is not its own: scoping decides what loads automatically,
// never what exists.
const RulesDirName = "rules"

// DefaultChannelKnowledgeTemplate is the initial content seeded into a new
// channel's knowledge directory. The placeholder {{name}} is replaced with
// the channel name before writing.
const DefaultChannelKnowledgeTemplate = `# {{name}} — Channel Knowledge

This file is loaded only when operating on the "{{name}}" channel.
Use it for channel-specific context, preferences, and notes.

Global memory (../../CLAUDE.md) is always loaded alongside this file.

## Rulebooks loaded on this channel

Each ` + "`@`" + ` line below pulls a rulebook from ../../rules/ into context on every turn of this
channel. Add one when a rulebook applies to most of the work here; a rulebook that applies to
occasional work belongs in the list underneath instead.

(none yet)

## Rulebooks available, not loaded

Every other rulebook in ../../rules/ can still be read at any time — it is simply not carried on
every turn. List the ones worth knowing about here, each with the work that should send you to it.

(none yet)

## Notes
(none yet)
`

// DefaultRulesReadme is seeded into the rules pool so the first rulebook written
// follows the same shape as the rest.
const DefaultRulesReadme = `# Rulebooks

One file per area of work (` + "`automations.md`" + `, ` + "`invoices.md`" + `). Each holds the standing decisions for
that area — the things that are not renegotiated every time.

A channel loads a rulebook by ` + "`@`" + `-importing it from that channel's CLAUDE.md, and can read any of
the others on demand. Nothing here is hidden from a channel; the import list only decides what arrives
without being asked for.

## The shape of an entry

` + "```" + `
## Never send an invoice before the work is signed off
Applies to: the email channel, any outgoing payment request

<Two or three sentences: what to do, and the part that is easy to get wrong.>

- why: <the user's own words, and the date it came up>
- check: <what a wrong one looks like, concretely enough to spot in a draft>
` + "```" + `

` + "`why:`" + ` is what stops a rule being dropped the first time it is inconvenient, so it records what the
user actually said rather than a paraphrase.

## Changing one

These are the user's standing decisions, not notes. Adding, changing or removing one needs the user to
say yes first — propose it in the channel and wait. Editing a rulebook without that is blocked.
`

// seedChannelKnowledge ensures the channel's knowledge directory exists under
// memoryDir/channels/<channelName>/ and seeds a CLAUDE.md if missing. It also
// ensures the shared rules pool exists. Returns the directory path, or empty
// string if memoryDir is empty.
func seedChannelKnowledge(memoryDir, channelName string) string {
	if memoryDir == "" || channelName == "" {
		return ""
	}

	seedRulesPool(memoryDir)

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

// seedRulesPool creates the shared rulebook directory and its README. The README
// is only written when absent — it explains the entry shape, and the user may
// have adjusted it.
func seedRulesPool(memoryDir string) {
	dir := filepath.Join(memoryDir, RulesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("failed to create rules dir", "dir", dir, "err", err)
		return
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		if writeErr := os.WriteFile(readme, []byte(DefaultRulesReadme), 0o600); writeErr != nil {
			slog.Warn("failed to seed rules README", "path", readme, "err", writeErr)
		}
	}
}
