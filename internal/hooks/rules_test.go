package hooks_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/hooks"
	"tclaw/internal/memorylayout"
)

func TestRulesGate(t *testing.T) {
	t.Run("refuses a write to a rulebook", func(t *testing.T) {
		bin, memoryDir := setup(t)

		code, out := runHook(t, bin, "rules-gate", hookEnv(memoryDir, "admin"), map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 2, code, "output: %s", out)
		require.Contains(t, out, "rule_propose")
		require.Contains(t, out, "invoices.md")
	})

	t.Run("allows a write anywhere else in memory", func(t *testing.T) {
		bin, memoryDir := setup(t)

		code, out := runHook(t, bin, "rules-gate", hookEnv(memoryDir, "admin"), map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "shopping-list.md")},
		})

		require.Equal(t, 0, code, "output: %s", out)
	})

	t.Run("allows a channel index that names a rulebook", func(t *testing.T) {
		bin, memoryDir := setup(t)

		// Choosing what a channel loads is the agent's own memory work — only the
		// rulebook itself needs the user.
		code, out := runHook(t, bin, "rules-gate", hookEnv(memoryDir, "admin"), map[string]any{
			"tool_name":  "Edit",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "channels", "admin", "CLAUDE.md")},
		})

		require.Equal(t, 0, code, "output: %s", out)
	})

	t.Run("passes when it cannot tell where memory is", func(t *testing.T) {
		bin, memoryDir := setup(t)

		code, out := runHook(t, bin, "rules-gate", []string{}, map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 0, code, "a guard with no memory dir must fail open: %s", out)
	})

	t.Run("passes on an unreadable payload", func(t *testing.T) {
		bin, memoryDir := setup(t)

		cmd := exec.Command(bin, "rules-gate")
		cmd.Env = append(os.Environ(), hookEnv(memoryDir, "admin")...)
		cmd.Stdin = nil
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "output: %s", out)
	})
}

func TestRulesIndex(t *testing.T) {
	t.Run("flags a rulebook no channel mentions", func(t *testing.T) {
		bin, memoryDir := setup(t)
		writeFile(t, filepath.Join(memoryDir, "rules", "invoices.md"), "## some rule")

		code, out := runHook(t, bin, "rules-index", hookEnv(memoryDir, "admin"), map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 0, code)
		require.Contains(t, out, "invoices.md")
		require.Contains(t, out, filepath.Join("channels", "admin", "CLAUDE.md"))
	})

	t.Run("says nothing when a channel already mentions it", func(t *testing.T) {
		bin, memoryDir := setup(t)
		writeFile(t, filepath.Join(memoryDir, "rules", "invoices.md"), "## some rule")
		writeFile(t, filepath.Join(memoryDir, "channels", "admin", "CLAUDE.md"),
			"# admin\n\n@../../rules/invoices.md\n")

		code, out := runHook(t, bin, "rules-index", hookEnv(memoryDir, "admin"), map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 0, code)
		require.Empty(t, out)
	})

	t.Run("ignores the pool's own README", func(t *testing.T) {
		bin, memoryDir := setup(t)
		writeFile(t, filepath.Join(memoryDir, "rules", "README.md"), "# Rulebooks")

		code, out := runHook(t, bin, "rules-index", hookEnv(memoryDir, "admin"), map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(memoryDir, "rules", "README.md")},
		})

		require.Equal(t, 0, code)
		require.Empty(t, out)
	})
}

func TestSettingsBlock(t *testing.T) {
	t.Run("registers every hook in the manifest", func(t *testing.T) {
		raw, err := hooks.SettingsBlock("/usr/local/bin/tclaw-hooks")
		require.NoError(t, err)

		var events map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		}
		require.NoError(t, json.Unmarshal(raw, &events))

		registered := map[string]string{}
		for event, groups := range events {
			for _, group := range groups {
				for _, hook := range group.Hooks {
					require.Equal(t, "command", hook.Type)
					// The command carries the path in full: hooks run under a shell
					// that reads no profile, so a variable here would expand to
					// nothing and fail on every tool call.
					require.Contains(t, hook.Command, `"/usr/local/bin/tclaw-hooks"`)
					require.NotContains(t, hook.Command, "$")
					name := hook.Command[len(`"/usr/local/bin/tclaw-hooks" `):]
					registered[name] = event
				}
			}
		}

		require.Len(t, registered, len(hooks.Manifest))
		for _, hook := range hooks.Manifest {
			require.Equal(t, hook.Event, registered[hook.Name], "hook %s", hook.Name)
		}
	})

	t.Run("rejects an empty binary path", func(t *testing.T) {
		_, err := hooks.SettingsBlock("")
		require.Error(t, err)
	})
}

// --- helpers ---

// setup builds the hook binary once per test and returns it with a fresh memory
// directory holding a rules pool and one channel.
func setup(t *testing.T) (string, string) {
	t.Helper()
	bin := buildHooks(t)
	memoryDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(memoryDir, "rules"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(memoryDir, "channels", "admin"), 0o700))
	return bin, memoryDir
}

// buildHooks compiles the hook binary. The guards are exercised through the real
// binary because what is being tested is how it behaves as a hook — its exit
// code and what it writes — not the functions underneath.
func buildHooks(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tclaw-hooks")
	build := exec.Command("go", "build", "-o", bin, "tclaw/cmd/tclaw-hooks")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build tclaw-hooks: %s", out)
	return bin
}

func hookEnv(memoryDir, channelName string) []string {
	return []string{
		memorylayout.EnvMemoryDir + "=" + memoryDir,
		memorylayout.EnvChannel + "=" + channelName,
	}
}

func runHook(t *testing.T, bin, name string, env []string, payload any) (int, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	cmd := exec.Command(bin, name)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "unexpected failure running %s: %s", name, out)
	return exit.ExitCode(), string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
