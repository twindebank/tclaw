package hooks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/hooks"
	"tclaw/internal/memorylayout"
)

func TestRulesGate(t *testing.T) {
	t.Run("refuses a write to a rulebook", func(t *testing.T) {
		h := setup(t)

		code, out := runHook(t, h, "rules-gate", "admin", map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 2, code, "output: %s", out)
		require.Contains(t, out, "rule_propose")
		require.Contains(t, out, "invoices.md")
	})

	t.Run("allows a write anywhere else in memory", func(t *testing.T) {
		h := setup(t)

		code, out := runHook(t, h, "rules-gate", "admin", map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "shopping-list.md")},
		})

		require.Equal(t, 0, code, "output: %s", out)
	})

	t.Run("allows a channel index that names a rulebook", func(t *testing.T) {
		h := setup(t)

		// Choosing what a channel loads is the agent's own memory work — only the
		// rulebook itself needs the user.
		code, out := runHook(t, h, "rules-gate", "admin", map[string]any{
			"tool_name":  "Edit",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "channels", "admin", "CLAUDE.md")},
		})

		require.Equal(t, 0, code, "output: %s", out)
	})

	t.Run("passes when it cannot tell where memory is", func(t *testing.T) {
		h := setup(t)

		code, out := runHook(t, withoutDirs(h), "rules-gate", "", map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 0, code, "a guard with no memory dir must fail open: %s", out)
	})

	t.Run("refuses a request it cannot read", func(t *testing.T) {
		h := setup(t)

		// The write might be aimed at a rulebook, and letting it through is the
		// one outcome the gate exists to prevent.
		code, out := postHook(t, h, "rules-gate", h.Server.Token(), "admin", []byte("not json"))

		require.Equal(t, 2, code, "output: %s", out)
		require.Contains(t, out, "could not read this tool call")
	})

	t.Run("refuses a request without the user's token", func(t *testing.T) {
		h := setup(t)
		body, err := json.Marshal(map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "shopping-list.md")},
		})
		require.NoError(t, err)

		code, out := postHook(t, h, "rules-gate", "not-the-token", "admin", body)

		require.Equal(t, 2, code, "output: %s", out)
		require.Contains(t, out, "not authenticated")
	})
}

func TestServer_UnknownHook(t *testing.T) {
	t.Run("passes a hook it does not know", func(t *testing.T) {
		h := setup(t)

		// A stale registration must not start refusing tool calls.
		code, out := postHook(t, h, "retired-hook", h.Server.Token(), "admin", []byte("{}"))

		require.Equal(t, 0, code)
		require.Empty(t, out)
	})
}

func TestRulesIndex(t *testing.T) {
	t.Run("flags a rulebook no channel mentions", func(t *testing.T) {
		h := setup(t)
		writeFile(t, filepath.Join(h.MemoryDir, "rules", "invoices.md"), "## some rule")

		code, out := runHook(t, h, "rules-index", "admin", map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 0, code)

		var advice struct {
			SystemMessage      string `json:"systemMessage"`
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &advice), "output: %s", out)

		require.Equal(t, "PostToolUse", advice.HookSpecificOutput.HookEventName)
		require.Contains(t, advice.HookSpecificOutput.AdditionalContext, "invoices.md")
		require.Contains(t, advice.HookSpecificOutput.AdditionalContext,
			filepath.Join("channels", "admin", "CLAUDE.md"))

		// The notice is what reaches the chat, so the user sees the hook acted.
		require.Contains(t, advice.SystemMessage, "rules-index")
		require.Contains(t, advice.SystemMessage, "invoices.md")
	})

	t.Run("says nothing when a channel already mentions it", func(t *testing.T) {
		h := setup(t)
		writeFile(t, filepath.Join(h.MemoryDir, "rules", "invoices.md"), "## some rule")
		writeFile(t, filepath.Join(h.MemoryDir, "channels", "admin", "CLAUDE.md"),
			"# admin\n\n@../../rules/invoices.md\n")

		code, out := runHook(t, h, "rules-index", "admin", map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "rules", "invoices.md")},
		})

		require.Equal(t, 0, code)
		require.Empty(t, out)
	})

	t.Run("ignores the pool's own README", func(t *testing.T) {
		h := setup(t)
		writeFile(t, filepath.Join(h.MemoryDir, "rules", "README.md"), "# Rulebooks")

		code, out := runHook(t, h, "rules-index", "admin", map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(h.MemoryDir, "rules", "README.md")},
		})

		require.Equal(t, 0, code)
		require.Empty(t, out)
	})
}

func TestSettingsBlock(t *testing.T) {
	t.Run("registers every hook in the manifest at the user's hook server", func(t *testing.T) {
		raw, err := hooks.SettingsBlock("http://127.0.0.1:4100")
		require.NoError(t, err)

		var events map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type           string            `json:"type"`
				URL            string            `json:"url"`
				Headers        map[string]string `json:"headers"`
				AllowedEnvVars []string          `json:"allowedEnvVars"`
			} `json:"hooks"`
		}
		require.NoError(t, json.Unmarshal(raw, &events))

		registered := map[string]hooks.HookEvent{}
		for event, groups := range events {
			for _, group := range groups {
				for _, hook := range group.Hooks {
					require.Equal(t, "http", hook.Type)
					require.Equal(t, "Bearer $"+memorylayout.EnvHookToken, hook.Headers["Authorization"])
					// The CLI only fills in variables the registration lists; any
					// other reference would be sent empty.
					require.ElementsMatch(t, []string{memorylayout.EnvHookToken, memorylayout.EnvChannel}, hook.AllowedEnvVars)
					name := strings.TrimPrefix(hook.URL, "http://127.0.0.1:4100/hooks/")
					registered[name] = hooks.HookEvent(event)
				}
			}
		}

		require.Len(t, registered, len(hooks.Manifest))
		for _, hook := range hooks.Manifest {
			require.Equal(t, hook.Event, registered[hook.Name], "hook %s", hook.Name)
		}
	})

	t.Run("rejects an empty server URL", func(t *testing.T) {
		_, err := hooks.SettingsBlock("")
		require.Error(t, err)
	})
}

// --- helpers ---

// harness is a running hook server and the directories one turn runs against.
type harness struct {
	Server    *hooks.Server
	URL       string
	MemoryDir string
	ConfigDir string
}

// setup starts a hook server over a fresh memory directory holding a rules pool
// and one channel, plus an empty config directory. The hooks are exercised over
// HTTP because what is being tested is how they answer the CLI.
func setup(t *testing.T) harness {
	t.Helper()
	memoryDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(memoryDir, "rules"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(memoryDir, "channels", "admin"), 0o700))
	return startServer(t, memoryDir, t.TempDir())
}

// withoutDirs is h's directories behind a server that was told neither of them.
func withoutDirs(h harness) harness {
	return harness{MemoryDir: h.MemoryDir, ConfigDir: h.ConfigDir, Server: nil}
}

func startServer(t *testing.T, memoryDir, configDir string) harness {
	t.Helper()
	server := hooks.NewServer(hooks.Env{MemoryDir: memoryDir, ConfigDir: configDir})
	url, err := server.Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop(context.Background())) })
	return harness{Server: server, URL: url, MemoryDir: memoryDir, ConfigDir: configDir}
}

// readInbox returns the rows the retro queue holds, newest last.
func readInbox(t *testing.T, configDir string) []queuedRow {
	t.Helper()
	raw, err := os.ReadFile(memorylayout.InboxPath(configDir))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)

	rows := []queuedRow{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var row queuedRow
		require.NoError(t, json.Unmarshal([]byte(line), &row), "row: %s", line)
		rows = append(rows, row)
	}
	return rows
}

// queuedRow is one line of the retro queue as a reader sees it.
type queuedRow struct {
	Timestamp string `json:"timestamp"`
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
	Kind      string `json:"kind"`
	Trigger   string `json:"trigger"`
	Detail    string `json:"detail"`
}

// runHook sends the hook a JSON payload as the CLI would, and returns 2 when it
// refused the call, as the old exit code did, or 0, with the response body.
func runHook(t *testing.T, h harness, name, channelName string, payload any) (int, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	if h.Server == nil {
		// A server told neither directory, in front of the same files.
		h = startServer(t, "", "")
	}
	return postHook(t, h, name, h.Server.Token(), channelName, body)
}

func postHook(t *testing.T, h harness, name, token, channelName string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.URL+"/hooks/"+name, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tclaw-Channel", channelName)
	rsp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer rsp.Body.Close()
	out, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rsp.StatusCode, "any other status lets the call through: %s", out)

	var decision struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if len(out) > 0 {
		require.NoError(t, json.Unmarshal(out, &decision), "output: %s", out)
	}
	if decision.HookSpecificOutput.PermissionDecision == "deny" {
		return 2, string(out)
	}
	return 0, string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
