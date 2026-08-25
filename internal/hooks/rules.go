package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tclaw/internal/memorylayout"
)

// rulesGate refuses a direct write to the rulebook pool. Rulebooks hold the
// user's standing decisions, so changing one needs the user to say yes — and the
// agent must not be able to answer that on its own. `rule_propose` asks in the
// channel and the router writes the file once the user replies, outside the
// sandbox, so there is no route from a tool call to a changed rule.
//
// Reading is untouched. Every rulebook stays readable from every channel; only
// writing is gated.
func rulesGate() {
	memoryDir := os.Getenv(memorylayout.EnvMemoryDir)
	if memoryDir == "" {
		pass() // nothing to compare against → fail open
	}
	p := readPayload()
	target := p.targetPath()
	if !memorylayout.InRules(memoryDir, target) {
		pass()
	}
	block(blockParams{
		Guard:     "rules-gate",
		SessionID: p.SessionID,
		Reason: fmt.Sprintf(`Refused: %s is a rulebook, and rulebooks are the user's standing decisions.

Proposing a change is your job and deciding is theirs. Use `+"`rule_propose`"+` with the full text you
want the file to have — it asks in this channel and writes the file only after the user replies "yes".

Reading is not restricted: you can read any rulebook in %s at any time, including ones this channel
does not load automatically.`, filepath.Base(target), memorylayout.RulesDir(memoryDir)),
	})
}

// rulesIndex notices a rulebook no channel mentions. A rulebook nothing points at
// is loaded by nobody and found by nobody, which reads exactly like having no
// rule at all — the failure this whole layer exists to avoid.
func rulesIndex() {
	memoryDir := os.Getenv(memorylayout.EnvMemoryDir)
	if memoryDir == "" {
		pass()
	}
	p := readPayload()
	target := p.targetPath()
	if !memorylayout.InRules(memoryDir, target) {
		pass()
	}
	name := filepath.Base(target)
	if name == "README.md" {
		pass() // the pool's own guide, not a rulebook
	}
	if referencedByAnyChannel(memoryDir, name) {
		pass()
	}
	channelName := os.Getenv(memorylayout.EnvChannel)
	if channelName == "" {
		channelName = "this channel"
	}
	advise(advice{
		Event: eventPostToolUse,
		Context: fmt.Sprintf(
			"No channel mentions %s, so no channel loads it and nobody will come across it. "+
				"Add it to %s: `@../../%s/%s` under the loaded list if it applies to most work there, "+
				"or a line under the available list with the work that should send you to it.",
			name, filepath.Join(memorylayout.ChannelsDirName, channelName, "CLAUDE.md"),
			memorylayout.RulesDirName, name),
		Notice: fmt.Sprintf("rules-index: no channel mentions %s, so nothing loads it", name),
	})
}

// referencedByAnyChannel reports whether any channel's CLAUDE.md names the
// rulebook, whether as an import or as a line in its available list.
func referencedByAnyChannel(memoryDir, name string) bool {
	indexes, err := filepath.Glob(filepath.Join(memoryDir, memorylayout.ChannelsDirName, "*", "CLAUDE.md"))
	if err != nil {
		return true // cannot tell → say nothing
	}
	for _, index := range indexes {
		raw, err := os.ReadFile(index)
		if err != nil {
			continue // an unreadable index is not evidence the rulebook is orphaned
		}
		if strings.Contains(string(raw), name) {
			return true
		}
	}
	return false
}
