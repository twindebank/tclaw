package ruletools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp"
	"tclaw/internal/tool/ruletools"
)

func TestRulePropose(t *testing.T) {
	t.Run("asks the user and writes nothing itself", func(t *testing.T) {
		h, memoryDir, armed := setup(t)

		result := callTool(t, h, "rule_propose", map[string]any{
			"file":    "invoices.md",
			"content": "## Never send an invoice before sign-off\n",
			"reason":  "the user asked for this twice",
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "awaiting_confirmation", got["status"])
		require.Equal(t, "invoices.md", got["file"])

		require.Len(t, *armed, 1)
		require.Equal(t, "invoices.md", (*armed)[0].File)
		require.Equal(t, "## Never send an invoice before sign-off\n", (*armed)[0].Content)

		// The tool must not write the file — only the user's reply does that.
		_, err := os.Stat(filepath.Join(memoryDir, "rules", "invoices.md"))
		require.True(t, os.IsNotExist(err), "rule_propose wrote the file without confirmation")
	})

	t.Run("rejects a path instead of a name", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "rule_propose", map[string]any{
			"file":    "../../home/.claude/settings.json",
			"content": "x",
			"reason":  "y",
		})
		require.Contains(t, err.Error(), "plain name")
	})

	t.Run("rejects a name that is not markdown", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "rule_propose", map[string]any{
			"file":    "invoices.txt",
			"content": "x",
			"reason":  "y",
		})
		require.Contains(t, err.Error(), ".md")
	})

	t.Run("requires content and a reason", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "rule_propose", map[string]any{
			"file": "invoices.md", "content": "  ", "reason": "y",
		})
		require.Contains(t, err.Error(), "content is required")

		err = callToolExpectError(t, h, "rule_propose", map[string]any{
			"file": "invoices.md", "content": "x", "reason": "",
		})
		require.Contains(t, err.Error(), "reason is required")
	})
}

func TestRuleList(t *testing.T) {
	t.Run("separates the rulebooks a channel loads from the ones it only lists", func(t *testing.T) {
		h, memoryDir, _ := setup(t)
		writeFile(t, filepath.Join(memoryDir, "rules", "invoices.md"), "## a rule")
		writeFile(t, filepath.Join(memoryDir, "rules", "frost.md"), "## another rule")
		writeFile(t, filepath.Join(memoryDir, "rules", "README.md"), "# Rulebooks")
		writeFile(t, filepath.Join(memoryDir, "channels", "email", "CLAUDE.md"),
			"# email\n\n@../../rules/invoices.md\n\nAvailable: frost.md when the weather comes up\n")
		writeFile(t, filepath.Join(memoryDir, "channels", "home", "CLAUDE.md"),
			"# home\n\n@../../rules/frost.md\n")

		result := callTool(t, h, "rule_list", map[string]any{})

		var got struct {
			Rulebooks []struct {
				File     string   `json:"file"`
				LoadedBy []string `json:"loaded_by"`
				ListedBy []string `json:"listed_by"`
			} `json:"rulebooks"`
		}
		require.NoError(t, json.Unmarshal(result, &got))

		require.Len(t, got.Rulebooks, 2, "README.md is the pool's guide, not a rulebook")
		require.Equal(t, "frost.md", got.Rulebooks[0].File)
		require.Equal(t, []string{"home"}, got.Rulebooks[0].LoadedBy)
		require.Equal(t, []string{"email"}, got.Rulebooks[0].ListedBy)
		require.Equal(t, "invoices.md", got.Rulebooks[1].File)
		require.Equal(t, []string{"email"}, got.Rulebooks[1].LoadedBy)
		require.Empty(t, got.Rulebooks[1].ListedBy)
	})

	t.Run("returns an empty list before any rulebook exists", func(t *testing.T) {
		h, _, _ := setup(t)

		result := callTool(t, h, "rule_list", map[string]any{})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Empty(t, got["rulebooks"])
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, string, *[]ruletools.RuleWriteRequest) {
	t.Helper()
	memoryDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(memoryDir, "rules"), 0o700))

	var armed []ruletools.RuleWriteRequest
	handler := mcp.NewHandler()
	ruletools.RegisterTools(handler, ruletools.Deps{
		MemoryDir: memoryDir,
		ArmRuleWrite: func(_ context.Context, request ruletools.RuleWriteRequest) error {
			armed = append(armed, request)
			return nil
		},
	})
	return handler, memoryDir, &armed
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
