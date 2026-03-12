package agent

import (
	"strings"
	"testing"

	"tclaw/channel"
	"tclaw/claudecli"
)

// --- isBuiltinAllowed tests ---

func TestIsBuiltinAllowed_NoBuiltins_AllowsEverything(t *testing.T) {
	// Backwards compat: no builtin__ entries → all builtins allowed.
	opts := Options{
		AllowedTools: []claudecli.Tool{"Bash", "Read"},
	}
	if !isBuiltinAllowed(opts, "ch1", claudecli.BuiltinStop) {
		t.Error("expected stop allowed when no builtins in list")
	}
	if !isBuiltinAllowed(opts, "ch1", claudecli.BuiltinResetAll) {
		t.Error("expected reset_all allowed when no builtins in list")
	}
}

func TestIsBuiltinAllowed_ExplicitBuiltin(t *testing.T) {
	opts := Options{
		AllowedTools: []claudecli.Tool{"Bash", claudecli.BuiltinStop},
	}
	if !isBuiltinAllowed(opts, "ch1", claudecli.BuiltinStop) {
		t.Error("expected stop allowed when explicitly listed")
	}
	if isBuiltinAllowed(opts, "ch1", claudecli.BuiltinCompact) {
		t.Error("expected compact denied when not in list (but other builtins present)")
	}
}

func TestIsBuiltinAllowed_ResetWildcard(t *testing.T) {
	opts := Options{
		AllowedTools: []claudecli.Tool{claudecli.BuiltinReset},
	}
	for _, cmd := range []claudecli.Tool{
		claudecli.BuiltinResetSession,
		claudecli.BuiltinResetMemories,
		claudecli.BuiltinResetProject,
		claudecli.BuiltinResetAll,
	} {
		if !isBuiltinAllowed(opts, "ch1", cmd) {
			t.Errorf("expected %s allowed via builtin__reset wildcard", cmd)
		}
	}
	// Non-reset builtins should still be denied.
	if isBuiltinAllowed(opts, "ch1", claudecli.BuiltinStop) {
		t.Error("expected stop denied when only builtin__reset is listed")
	}
}

func TestIsBuiltinAllowed_ChannelOverride(t *testing.T) {
	opts := Options{
		AllowedTools: []claudecli.Tool{claudecli.BuiltinStop, claudecli.BuiltinCompact},
		ChannelToolOverrides: map[channel.ChannelID]ChannelToolPermissions{
			"restricted": {
				AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
			},
		},
	}
	// Channel with override: only stop allowed.
	if !isBuiltinAllowed(opts, "restricted", claudecli.BuiltinStop) {
		t.Error("expected stop allowed on restricted channel")
	}
	if isBuiltinAllowed(opts, "restricted", claudecli.BuiltinCompact) {
		t.Error("expected compact denied on restricted channel")
	}
	// Channel without override: falls back to user-level.
	if !isBuiltinAllowed(opts, "other", claudecli.BuiltinCompact) {
		t.Error("expected compact allowed on non-overridden channel")
	}
}

// --- resolveToolsForChannel tests ---

func TestResolveToolsForChannel_UserLevel(t *testing.T) {
	opts := Options{
		AllowedTools:    []claudecli.Tool{"Bash", "Read", claudecli.BuiltinStop},
		DisallowedTools: []claudecli.Tool{"Write", claudecli.BuiltinCompact},
	}
	allowed, disallowed := resolveToolsForChannel(opts, "ch1")

	// Builtins should be filtered out.
	for _, tool := range allowed {
		if claudecli.IsBuiltinTool(tool) {
			t.Errorf("builtin %s should be filtered from allowed", tool)
		}
	}
	for _, tool := range disallowed {
		if claudecli.IsBuiltinTool(tool) {
			t.Errorf("builtin %s should be filtered from disallowed", tool)
		}
	}

	if len(allowed) != 2 {
		t.Errorf("expected 2 allowed tools (Bash, Read), got %d: %v", len(allowed), allowed)
	}
	if len(disallowed) != 1 {
		t.Errorf("expected 1 disallowed tool (Write), got %d: %v", len(disallowed), disallowed)
	}
}

func TestResolveToolsForChannel_ChannelOverride(t *testing.T) {
	opts := Options{
		AllowedTools: []claudecli.Tool{"Bash", "Read"},
		ChannelToolOverrides: map[channel.ChannelID]ChannelToolPermissions{
			"restricted": {
				AllowedTools: []claudecli.Tool{"Read", claudecli.BuiltinResetSession},
			},
		},
	}
	allowed, _ := resolveToolsForChannel(opts, "restricted")

	// Should use channel override, not user-level.
	if len(allowed) != 1 {
		t.Errorf("expected 1 allowed tool (Read, minus builtin), got %d: %v", len(allowed), allowed)
	}
	if allowed[0] != "Read" {
		t.Errorf("expected Read, got %s", allowed[0])
	}
}

// --- resolveResetChoice tests ---

func TestResolveResetChoice_DynamicNumbering(t *testing.T) {
	// Only session and memories allowed.
	levels := []ResetLevel{ResetSession, ResetMemories}

	if got := resolveResetChoice("1", levels); got != ResetSession {
		t.Errorf("choice '1' = %d, want ResetSession", got)
	}
	if got := resolveResetChoice("2", levels); got != ResetMemories {
		t.Errorf("choice '2' = %d, want ResetMemories", got)
	}
	// 3 is cancel (len(levels)+1).
	if got := resolveResetChoice("3", levels); got != resetCancel {
		t.Errorf("choice '3' = %d, want resetCancel", got)
	}
	// 4 is out of range.
	if got := resolveResetChoice("4", levels); got != resetInvalid {
		t.Errorf("choice '4' = %d, want resetInvalid", got)
	}
}

func TestResolveResetChoice_WordAliases(t *testing.T) {
	levels := []ResetLevel{ResetSession, ResetMemories}

	if got := resolveResetChoice("session", levels); got != ResetSession {
		t.Errorf("choice 'session' = %d, want ResetSession", got)
	}
	if got := resolveResetChoice("memories", levels); got != ResetMemories {
		t.Errorf("choice 'memories' = %d, want ResetMemories", got)
	}
	// "project" not in levels → invalid.
	if got := resolveResetChoice("project", levels); got != resetInvalid {
		t.Errorf("choice 'project' = %d, want resetInvalid (not in levels)", got)
	}
	if got := resolveResetChoice("cancel", levels); got != resetCancel {
		t.Errorf("choice 'cancel' = %d, want resetCancel", got)
	}
}

// --- allowedResetLevels tests ---

func TestAllowedResetLevels_NoBuiltins_AllLevels(t *testing.T) {
	// No builtins → all levels allowed (backwards compat).
	opts := Options{
		AllowedTools: []claudecli.Tool{"Bash"},
	}
	levels := allowedResetLevels(opts, "ch1")
	if len(levels) != 4 {
		t.Errorf("expected 4 levels (all), got %d: %v", len(levels), levels)
	}
}

func TestAllowedResetLevels_OnlySessionAndMemories(t *testing.T) {
	opts := Options{
		AllowedTools: []claudecli.Tool{
			claudecli.BuiltinResetSession,
			claudecli.BuiltinResetMemories,
			claudecli.BuiltinStop,
		},
	}
	levels := allowedResetLevels(opts, "ch1")
	if len(levels) != 2 {
		t.Errorf("expected 2 levels, got %d: %v", len(levels), levels)
	}
	if levels[0] != ResetSession || levels[1] != ResetMemories {
		t.Errorf("expected [Session, Memories], got %v", levels)
	}
}

func TestAllowedResetLevels_ResetWildcard_AllLevels(t *testing.T) {
	opts := Options{
		AllowedTools: []claudecli.Tool{claudecli.BuiltinReset},
	}
	levels := allowedResetLevels(opts, "ch1")
	if len(levels) != 4 {
		t.Errorf("expected 4 levels via wildcard, got %d: %v", len(levels), levels)
	}
}

func TestAllowedResetLevels_SessionAlwaysIncluded(t *testing.T) {
	// Even if no reset builtins match, Session is always included.
	opts := Options{
		AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
	}
	levels := allowedResetLevels(opts, "ch1")
	if len(levels) != 1 || levels[0] != ResetSession {
		t.Errorf("expected [Session] as minimum, got %v", levels)
	}
}

// --- Dynamic reset menu tests ---

func TestDynamicResetMenuPrompt_RestrictedLevels(t *testing.T) {
	levels := []ResetLevel{ResetSession, ResetMemories}
	menu := dynamicResetMenuPrompt(levels, channel.MarkupMarkdown)

	if !strings.Contains(menu, "Session") {
		t.Error("menu should contain Session")
	}
	if !strings.Contains(menu, "Memories") {
		t.Error("menu should contain Memories")
	}
	if strings.Contains(menu, "Project") {
		t.Error("menu should NOT contain Project when restricted")
	}
	if strings.Contains(menu, "Everything") {
		t.Error("menu should NOT contain Everything when restricted")
	}
	// Cancel should be option 3 (2 levels + 1).
	if !strings.Contains(menu, "3") {
		t.Error("cancel should be option 3")
	}
}

// --- Builtin command denial integration test ---

func TestStop_DeniedOnRestrictedChannel(t *testing.T) {
	opts := Options{
		ChannelToolOverrides: map[channel.ChannelID]ChannelToolPermissions{
			"test-ch": {
				AllowedTools: []claudecli.Tool{claudecli.BuiltinCompact},
			},
		},
	}
	_, sends := sendMessages(t, opts, "stop")

	found := false
	for _, s := range sends {
		if strings.Contains(s, "not available") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected denial message for stop, got: %v", sends)
	}
}

func TestReset_DeniedOnRestrictedChannel(t *testing.T) {
	opts := Options{
		ChannelToolOverrides: map[channel.ChannelID]ChannelToolPermissions{
			"test-ch": {
				// No reset builtins at all, but has some builtin
				// so backwards compat doesn't kick in.
				AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
			},
		},
	}
	_, sends := sendMessages(t, opts, "reset")

	// Reset should still be allowed (session is always included),
	// but only show session + cancel.
	found := false
	for _, s := range sends {
		if strings.Contains(s, "Reset") && strings.Contains(s, "Session") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reset menu with at least Session, got: %v", sends)
	}

	// Should NOT contain Project or Everything.
	for _, s := range sends {
		if strings.Contains(s, "Project") || strings.Contains(s, "Everything") {
			t.Errorf("restricted reset menu should not contain Project/Everything, got: %s", s)
		}
	}
}

// --- expandMCPGlobs tests ---

func TestExpandMCPGlobs(t *testing.T) {
	mcpTools := []string{
		"channel_create", "channel_edit", "channel_delete", "channel_list",
		"schedule_create", "schedule_list",
		"google_workspace", "google_workspace_schema", "google_gmail_list",
		"connection_add", "connection_list",
	}

	t.Run("expands glob to matching MCP tools", func(t *testing.T) {
		tools := []claudecli.Tool{"mcp__tclaw__channel_*"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 4 {
			t.Fatalf("expected 4 expanded tools, got %d: %v", len(got), got)
		}
		for _, g := range got {
			if !strings.HasPrefix(string(g), "mcp__tclaw__channel_") {
				t.Errorf("unexpected tool %q", g)
			}
		}
	})

	t.Run("expands google glob", func(t *testing.T) {
		tools := []claudecli.Tool{"mcp__tclaw__google_*"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 3 {
			t.Fatalf("expected 3 google tools, got %d: %v", len(got), got)
		}
	})

	t.Run("non-glob tools pass through unchanged", func(t *testing.T) {
		tools := []claudecli.Tool{"Bash", "Read", "mcp__tclaw__channel_create"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 3 {
			t.Fatalf("expected 3 tools, got %d: %v", len(got), got)
		}
		if got[0] != "Bash" || got[1] != "Read" || got[2] != "mcp__tclaw__channel_create" {
			t.Errorf("unexpected tools: %v", got)
		}
	})

	t.Run("mixed globs and explicit tools", func(t *testing.T) {
		tools := []claudecli.Tool{"Bash", "mcp__tclaw__schedule_*", "WebSearch"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 4 {
			t.Fatalf("expected 4 tools (Bash + 2 schedule + WebSearch), got %d: %v", len(got), got)
		}
	})

	t.Run("unmatched glob preserved for non-MCP tools", func(t *testing.T) {
		tools := []claudecli.Tool{"Bash*"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 1 || got[0] != "Bash*" {
			t.Errorf("expected unmatched glob preserved, got: %v", got)
		}
	})

	t.Run("nil MCP tools returns input unchanged", func(t *testing.T) {
		tools := []claudecli.Tool{"mcp__tclaw__channel_*", "Bash"}
		got := expandMCPGlobs(tools, nil)
		if len(got) != 2 {
			t.Fatalf("expected 2 tools, got %d: %v", len(got), got)
		}
	})
}
