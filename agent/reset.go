package agent

import (
	"fmt"
	"strconv"

	"tclaw/channel"
	"tclaw/claudecli"
)

// ResetLevel identifies what to clear when the user resets.
type ResetLevel int

const (
	// ResetSession clears the current channel's conversation session.
	ResetSession ResetLevel = iota

	// ResetMemories erases all memory files (CLAUDE.md, topic files).
	// Shared across all channels — this is user-level, not per-channel.
	ResetMemories

	// ResetProject clears Claude Code state and all sessions, but keeps
	// memory files, connections, schedules, and secrets.
	ResetProject

	// ResetAll erases everything in user space — memory, Claude state,
	// sessions, connections, schedules, and secrets.
	ResetAll

	// resetCancel is a sentinel used by resolveResetChoice — not a real level.
	resetCancel ResetLevel = -1

	// resetInvalid is returned when the user's input doesn't match any option.
	resetInvalid ResetLevel = -2
)

// resetState tracks the multi-step reset flow.
type resetState int

const (
	resetChoosing   resetState = iota // waiting for user to pick a level
	resetConfirming                   // waiting for user to type "confirm"
)

// pendingReset tracks per-channel reset flow state.
type pendingReset struct {
	state resetState
	level ResetLevel // set once user picks a level that needs confirmation
}

// allResetLevels is the full ordered list of reset levels.
var allResetLevels = []ResetLevel{ResetSession, ResetMemories, ResetProject, ResetAll}

// resetLevelBuiltin maps each reset level to its builtin tool constant.
var resetLevelBuiltin = map[ResetLevel]claudecli.Tool{
	ResetSession:  claudecli.BuiltinResetSession,
	ResetMemories: claudecli.BuiltinResetMemories,
	ResetProject:  claudecli.BuiltinResetProject,
	ResetAll:      claudecli.BuiltinResetAll,
}

// allowedResetLevels returns the reset levels permitted on the given channel.
// Session is always included (it's the most basic reset). Other levels are
// included if their corresponding builtin tool is allowed.
func allowedResetLevels(opts Options, channelID channel.ChannelID) []ResetLevel {
	var levels []ResetLevel
	for _, level := range allResetLevels {
		builtin := resetLevelBuiltin[level]
		if isBuiltinAllowed(opts, channelID, builtin) {
			levels = append(levels, level)
		}
	}
	// Session is always allowed as a minimum.
	if len(levels) == 0 {
		levels = []ResetLevel{ResetSession}
	}
	return levels
}

// resetMenuPrompt builds the static numbered reset menu (all levels).
// Kept for backwards compatibility in tests.
func resetMenuPrompt(m channel.Markup) string {
	return dynamicResetMenuPrompt(allResetLevels, m)
}

// dynamicResetMenuPrompt builds a numbered reset menu from the given levels.
// Cancel is always appended as the last option.
func dynamicResetMenuPrompt(levels []ResetLevel, m channel.Markup) string {
	s := "🔄 " + bold(m, "Reset") + "\n\nChoose what to clear:\n"
	for i, level := range levels {
		num := fmt.Sprintf("%d", i+1)
		s += bold(m, num) + " — " + resetLevelMenuLine(level, m) + "\n"
	}
	cancelNum := fmt.Sprintf("%d", len(levels)+1)
	s += bold(m, cancelNum) + " — Cancel"
	return s
}

// resetLevelMenuLine returns the description for a reset level in the menu.
func resetLevelMenuLine(level ResetLevel, m channel.Markup) string {
	switch level {
	case ResetSession:
		return bold(m, "Session") + " — clear this channel's conversation only (other channels keep their sessions)"
	case ResetMemories:
		return bold(m, "Memories") + " — erase all memory files (CLAUDE.md + topic files, shared across all channels)"
	case ResetProject:
		return bold(m, "Project") + " — clear Claude state + sessions on " + bold(m, "all") + " channels (keeps memories, connections, schedules, API keys)"
	case ResetAll:
		return bold(m, "Everything") + " — erase all data including API keys, OAuth connections, channel tokens, and schedules"
	default:
		return "unknown"
	}
}

// resolveResetChoice maps user input to a reset level based on the dynamically
// numbered menu. Accepts both number and word aliases.
func resolveResetChoice(choice string, levels []ResetLevel) ResetLevel {
	// Word aliases.
	switch choice {
	case "session":
		return findInLevels(ResetSession, levels)
	case "memories":
		return findInLevels(ResetMemories, levels)
	case "project":
		return findInLevels(ResetProject, levels)
	case "everything", "all":
		return findInLevels(ResetAll, levels)
	case "cancel":
		return resetCancel
	}

	// Number input.
	n, err := strconv.Atoi(choice)
	if err != nil {
		return resetInvalid
	}
	// Cancel is the last numbered option.
	if n == len(levels)+1 {
		return resetCancel
	}
	if n >= 1 && n <= len(levels) {
		return levels[n-1]
	}
	return resetInvalid
}

// findInLevels returns the level if it's in the allowed list, otherwise resetInvalid.
func findInLevels(level ResetLevel, levels []ResetLevel) ResetLevel {
	for _, l := range levels {
		if l == level {
			return level
		}
	}
	return resetInvalid
}

// resetConfirmPrompt builds the confirmation prompt for destructive reset levels.
// Each level's prompt explicitly lists what will and won't be deleted,
// including how it affects other channels.
func resetConfirmPrompt(level ResetLevel, m channel.Markup) string {
	var lines string
	switch level {
	case ResetMemories:
		lines = bold(m, "Erased:") + " CLAUDE.md and all topic files (shared across all channels)\n" +
			bold(m, "Kept:") + " conversation sessions, OAuth connections, schedules, API keys, channel tokens\n\n" +
			"A fresh CLAUDE.md will be created on restart."

	case ResetProject:
		lines = bold(m, "Erased:") + "\n" +
			"  • Conversation sessions on " + bold(m, "all channels") + " (not just this one)\n" +
			"  • Claude Code internal state (conversation history, plans, settings)\n\n" +
			bold(m, "Kept:") + " memory files, OAuth connections, schedules, API keys, channel tokens"

	case ResetAll:
		lines = bold(m, "Erased:") + "\n" +
			"  • Memory files (CLAUDE.md + topic files)\n" +
			"  • Conversation sessions on " + bold(m, "all channels") + "\n" +
			"  • Claude Code internal state\n" +
			"  • OAuth connections (Google Workspace, etc.)\n" +
			"  • Schedules\n" +
			"  • API keys (Anthropic key, setup token)\n" +
			"  • Channel tokens (Telegram bot tokens for dynamic channels)\n\n" +
			"You will need to re-authenticate and reconnect all services."
	}

	return "⚠️ " + bold(m, "Confirm reset") + "\n\n" +
		lines + "\n\n" +
		"Type " + code(m, "confirm") + " to proceed or anything else to cancel."
}

// resetLevelName returns a human-readable name for a reset level.
func resetLevelName(level ResetLevel) string {
	switch level {
	case ResetSession:
		return "session"
	case ResetMemories:
		return "memories"
	case ResetProject:
		return "project"
	case ResetAll:
		return "everything"
	default:
		return "unknown"
	}
}
