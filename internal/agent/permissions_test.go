package agent

import (
	"strings"
	"testing"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
)

func TestIsBuiltinAllowed(t *testing.T) {
	t.Run("no builtins denies everything", func(t *testing.T) {
		// Without explicit builtin groups, no builtins are available.
		opts := Options{
			AllowedTools: []claudecli.Tool{"Bash", "Read"},
		}
		if isBuiltinAllowed(opts, "ch1", claudecli.BuiltinStop) {
			t.Error("expected stop denied when no builtins in list")
		}
		if isBuiltinAllowed(opts, "ch1", claudecli.BuiltinResetAll) {
			t.Error("expected reset_all denied when no builtins in list")
		}
	})

	t.Run("explicit builtin", func(t *testing.T) {
		opts := Options{
			AllowedTools: []claudecli.Tool{"Bash", claudecli.BuiltinStop},
		}
		if !isBuiltinAllowed(opts, "ch1", claudecli.BuiltinStop) {
			t.Error("expected stop allowed when explicitly listed")
		}
		if isBuiltinAllowed(opts, "ch1", claudecli.BuiltinCompact) {
			t.Error("expected compact denied when not in list (but other builtins present)")
		}
	})

	t.Run("reset wildcard", func(t *testing.T) {
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
	})

	t.Run("channel override", func(t *testing.T) {
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
	})
}

func TestResolveToolsForChannel(t *testing.T) {
	t.Run("user level", func(t *testing.T) {
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
	})

	t.Run("channel override", func(t *testing.T) {
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
	})
}

func TestResolveResetChoice(t *testing.T) {
	t.Run("dynamic numbering", func(t *testing.T) {
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
	})

	t.Run("word aliases", func(t *testing.T) {
		levels := []ResetLevel{ResetSession, ResetMemories}

		if got := resolveResetChoice("session", levels); got != ResetSession {
			t.Errorf("choice 'session' = %d, want ResetSession", got)
		}
		if got := resolveResetChoice("memories", levels); got != ResetMemories {
			t.Errorf("choice 'memories' = %d, want ResetMemories", got)
		}
		// "project" not in levels -> invalid.
		if got := resolveResetChoice("project", levels); got != resetInvalid {
			t.Errorf("choice 'project' = %d, want resetInvalid (not in levels)", got)
		}
		if got := resolveResetChoice("cancel", levels); got != resetCancel {
			t.Errorf("choice 'cancel' = %d, want resetCancel", got)
		}
	})
}

func TestAllowedResetLevels(t *testing.T) {
	t.Run("no builtins gives no levels", func(t *testing.T) {
		// Without explicit builtin groups, no reset levels are available.
		opts := Options{
			AllowedTools: []claudecli.Tool{"Bash"},
		}
		levels := allowedResetLevels(opts, "ch1")
		if len(levels) != 0 {
			t.Errorf("expected 0 levels, got %d: %v", len(levels), levels)
		}
	})

	t.Run("only session and memories", func(t *testing.T) {
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
	})

	t.Run("reset wildcard gives all levels", func(t *testing.T) {
		opts := Options{
			AllowedTools: []claudecli.Tool{claudecli.BuiltinReset},
		}
		levels := allowedResetLevels(opts, "ch1")
		if len(levels) != 4 {
			t.Errorf("expected 4 levels via wildcard, got %d: %v", len(levels), levels)
		}
	})

	t.Run("no reset builtins gives empty", func(t *testing.T) {
		// Only non-reset builtins → no reset levels available.
		opts := Options{
			AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
		}
		levels := allowedResetLevels(opts, "ch1")
		if len(levels) != 0 {
			t.Errorf("expected empty levels, got %v", levels)
		}
	})
}

func TestDynamicResetMenuPrompt(t *testing.T) {
	t.Run("restricted levels", func(t *testing.T) {
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
	})
}

func TestStop(t *testing.T) {
	t.Run("denied on restricted channel", func(t *testing.T) {
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
	})
}

func TestResetPermission(t *testing.T) {
	t.Run("denied when no reset builtins", func(t *testing.T) {
		opts := Options{
			ChannelToolOverrides: map[channel.ChannelID]ChannelToolPermissions{
				"test-ch": {
					// Has stop but no reset builtins — reset command should be denied.
					AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
				},
			},
		}
		_, sends := sendMessages(t, opts, "reset")

		// Should get the denial message, not a reset menu.
		found := false
		for _, s := range sends {
			if strings.Contains(s, "not available") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected denial message, got: %v", sends)
		}
	})
}

func TestExpandMCPGlobs(t *testing.T) {
	// MCPToolNames returns fully-qualified identifiers — mcp__<server>__<tool>
	// for the local tclaw server and every registered remote MCP. The list
	// below mixes both so tests cover the end-to-end property: globs expand
	// across ALL known MCP servers, not just tclaw.
	mcpTools := []string{
		"mcp__tclaw__channel_create", "mcp__tclaw__channel_edit",
		"mcp__tclaw__channel_delete", "mcp__tclaw__channel_list",
		"mcp__tclaw__schedule_create", "mcp__tclaw__schedule_list",
		"mcp__tclaw__google_workspace", "mcp__tclaw__google_workspace_schema",
		"mcp__tclaw__google_gmail_list",
		"mcp__tclaw__connection_add", "mcp__tclaw__connection_list",
		"mcp__home-assistant__ha_state_get", "mcp__home-assistant__ha_service_call",
		"mcp__home-assistant__ha_state_list",
	}

	t.Run("expands tclaw channel glob", func(t *testing.T) {
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

	t.Run("expands remote MCP glob", func(t *testing.T) {
		tools := []claudecli.Tool{"mcp__home-assistant__*"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 3 {
			t.Fatalf("expected 3 home-assistant tools, got %d: %v", len(got), got)
		}
		for _, g := range got {
			if !strings.HasPrefix(string(g), "mcp__home-assistant__") {
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

	t.Run("mixed globs across local and remote", func(t *testing.T) {
		tools := []claudecli.Tool{"Bash", "mcp__tclaw__schedule_*", "mcp__home-assistant__*", "WebSearch"}
		got := expandMCPGlobs(tools, mcpTools)
		// Bash + 2 schedule + 3 ha + WebSearch = 7
		if len(got) != 7 {
			t.Fatalf("expected 7 tools (Bash + 2 schedule + 3 ha + WebSearch), got %d: %v", len(got), got)
		}
	})

	t.Run("unmatched glob preserved for non-MCP tools", func(t *testing.T) {
		tools := []claudecli.Tool{"Bash*"}
		got := expandMCPGlobs(tools, mcpTools)
		if len(got) != 1 || got[0] != "Bash*" {
			t.Errorf("expected unmatched glob preserved, got: %v", got)
		}
	})

	t.Run("unmatched MCP glob preserved when server has no known tools", func(t *testing.T) {
		tools := []claudecli.Tool{"mcp__unknown-server__*"}
		got := expandMCPGlobs(tools, mcpTools)
		// No matches → raw glob kept. CLI won't match wildcards for MCP
		// tools, so nothing on that server will actually be allowed. The
		// registration code enforces we always have ToolNames, so in
		// practice this branch only fires for misconfigured input.
		if len(got) != 1 || got[0] != "mcp__unknown-server__*" {
			t.Errorf("expected raw glob preserved, got: %v", got)
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
