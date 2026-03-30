# Go Patterns

## Comments
- **Comments should explain "why" not "what"** — the code itself explains what it does
- **Comments should describe the thing itself, not the change** — write comments that make sense to future readers, not as PR context. Use "Higher RPS to handle worker registration burst" not "Increase RPS from default"
- **Never remove helpful comments when making changes** — preserve existing comments that explain context, expected behavior, or edge cases
- **Especially preserve comments when refactoring** — when extracting code into new functions/constants, MOVE the original comments with the code. The detailed reasoning is more valuable than a brief description.
- **Comments inside if/switch/case blocks, not above them** — place explanatory comments inside the block body, not as a preceding line above the condition:
  ```go
  // CORRECT
  if alreadyDone {
      // duplicate event, skip
      return nil
  }

  // WRONG
  // duplicate event, skip
  if alreadyDone {
      return nil
  }
  ```
- **Struct field comments go above fields** with blank lines separating commented fields for readability
- **Comment line width ~120 characters**

## Error Handling

> **The golden rule: every error must be visible. No exceptions.**

- **Never use `_` to discard errors** — every function call that returns an error must be handled
- **Never swallow errors silently** — if you can't return an error, you MUST log it at `ERROR` or `WARN` level. Never use bare `continue`, bare `return`, or empty `if err != nil {}` blocks without logging.
- **Never let unexpected cases pass silently** — every unexpected/impossible case must either return an error or log an error. Never return zero values for unexpected states without signaling the problem. This applies to:
  - Switch `default` cases — don't silently return nil
  - Functions that find "no results" when results were expected — return an error
  - Any branch that "shouldn't happen" — make it visible via error or log
- **Caller must be informed** — if an operation fails partway through, the caller must receive either an error return or an explicit signal in the data. Silent partial failure is the worst kind of bug: the caller proceeds as if nothing happened.
- **Use simple `if err != nil` for single error checks** — don't use switch statements when there's only one condition
- **Use switch for multiple error types** — `switch { case errors.Is(err, X): ... case err != nil: ... }`
- **Wrap errors with context** — `fmt.Errorf("context: %w", err)` to build a traceable error chain
- **Never return data alongside an error** — on error paths, return zero values for all non-error returns

```go
// WRONG — silent swallow, caller has no idea the operation failed
if err != nil {
    return // ❌ caller proceeds as if nothing happened
}

// WRONG — logged but caller still proceeds as if media was absent, not failed
path, err := downloadMedia(msg)
if err != nil {
    slog.Warn("failed", "err", err) // ❌ media silently dropped, user never knows
}

// CORRECT — return error up the stack wherever possible
if err != nil {
    return fmt.Errorf("download media: %w", err) // ✅ caller decides what to do
}

// CORRECT — when returning an error isn't possible, signal failure explicitly in output
path, err := downloadMedia(msg)
if err != nil {
    slog.Error("failed to download media", "err", err)
    text = formatMediaError(text, err) // ✅ agent sees the failure, can tell the user
}

// CORRECT — best-effort cleanup functions may swallow, but must still log
entries, err := os.ReadDir(dir)
if err != nil {
    slog.Warn("failed to read dir for cleanup", "dir", dir, "err", err) // ✅ at least visible
    return
}
```

## Testing

### General Rules
- **Use testify `require`** (not `assert`, not suites, not `t.Fatalf`) — `require.NoError(t, err)`, `require.Equal(t, expected, actual)`
- **Use `t.Run` for subtests** — keeps test output organized and allows targeted test runs
- **Table-driven tests for simple cases** — `tests := []struct{name, input, expected}` with a range loop
- **Individual `t.Run` for complex cases** — when tests have different assertion logic or setup
- **Test actual behavior, not implementation** — test what functions DO with inputs, not that you can create objects or set fields. Don't test what you just set.
- **Test the public contract** — test what callers will actually call and expect
- **Validate all expected outputs** — don't just check that a function doesn't error, verify the actual results
- **Check errors first** — `require.Error(t, err)` before checking error content
- **Helper functions go at the bottom** of test files, after a `// --- helpers ---` comment
- **Run tests**: `go test ./...` or `go test -v -run TestName ./path/to/package/...`

### Package Naming
- **Use `_test` suffix** (`package foo_test`) for MCP tool tests and anything that tests the public API from the outside. This ensures you're testing the exported interface, not relying on internal access.
- **Use same package** (`package foo`) only when you need to test unexported functions directly (e.g. `git_test.go` testing internal git helpers).

### Test File Structure
Every test file follows the same layout:
1. Test functions at the top
2. `// --- helpers ---` comment
3. Setup functions (`setup()`, `setupHarness()`, etc.)
4. Tool call helpers (`callTool()`, `callToolExpectError()`)
5. Mock types at the bottom

### MCP Tool Test Pattern
This is the primary test pattern in the codebase. All MCP tool tests follow this exact structure (see `channeltools_test.go`, `scheduletools_test.go`):

```go
package mytools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/tool/mytools"
)

func TestMyTool_DoesAThing(t *testing.T) {
	t.Run("success case", func(t *testing.T) {
		h, myStore := setup(t)

		result := callTool(t, h, "my_tool", map[string]any{
			"name": "value",
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "expected", got["field"])

		// Verify side effects in the store.
		item, err := myStore.Get(context.Background(), "key")
		require.NoError(t, err)
		require.NotNil(t, item)
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "my_tool", map[string]any{
			"name": "",
		})
		require.Contains(t, err.Error(), "required")
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *mypackage.Store) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	myStore := mypackage.NewStore(s)
	handler := mcp.NewHandler()
	mytools.RegisterTools(handler, mytools.Deps{
		Store: myStore,
	})

	return handler, myStore
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
```

### Mock Patterns
- **Only mock interfaces you don't own or that do I/O** — use real `store.NewFS(t.TempDir())` for stores, real handlers for MCP
- **In-memory mocks for secret.Store** — simple map-backed implementation:
  ```go
  type memorySecretStore struct {
  	data map[string]string
  }

  func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
  	return m.data[key], nil
  }

  func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
  	m.data[key] = value
  	return nil
  }

  func (m *memorySecretStore) Delete(_ context.Context, key string) error {
  	delete(m.data, key)
  	return nil
  }
  ```
- **Never mock the filesystem** — use `t.TempDir()` for real temp directories
- **Never mock MCP handlers** — use real `mcp.NewHandler()` with `RegisterTools()`

### Git Integration Tests
When testing code that calls git commands, create real repos in temp dirs:
```go
func createTestRemote(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch", branch)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.txt"), []byte("hello"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial commit")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, string(out))
}
```

### Test Naming
- **Pattern**: `Test{Subject}_{Scenario}` — e.g. `TestRepoSync_FullLifecycle`, `TestChannelCreate`
- When a test function uses `t.Run` for multiple scenarios, the top-level name is just `Test{Subject}` and scenarios go in the subtests

## Function Design
- **Prefer returning new values over mutating inputs** — makes data flow clearer
- **Prefer param structs over multiple parameters** — keeps signatures clean and extensible
- **Never put context.Context in param structs** — always pass it as the first function parameter, separate from the struct
- **Helper functions go at the bottom of files** — after the main logic
- **Search for existing patterns first** — look for similar implementations before writing new code

## Naming
- **Don't shorten/abbreviate names** — use full words for packages, variables, functions
- **Inline struct creation** when used once — don't create unnecessary intermediate variables
- **Self-documenting function/method names** — choose clear, descriptive names that eliminate the need for comments

## Adding New Tool Packages

Tool packages implement the `toolpkg.Package` interface (see `tool/toolpkg/toolpkg.go`). This makes adding a new tool package a single-package operation:

1. Create a new directory under `tool/` (e.g. `tool/mytools/`)
2. Implement the `toolpkg.Package` interface:
   - `Name()` — stable identifier (e.g. "mytool")
   - `Description()` — human-readable summary
   - `Group()` — primary toolgroup (used for display in info tools)
   - `GroupTools()` — maps toolgroups to CLI glob patterns. Most packages return one entry; packages that span multiple groups return multiple.
   - `RequiredSecrets()` — what secrets this package needs (with env var prefix for seeding)
   - `Info()` — returns structured `PackageInfo` with credential status
   - `Register()` — registers tools on the MCP handler
3. Add the package to the registry in `tool/all/all.go`

That's it — the registry handles everything else:
- Auto-generates a `<name>_info` tool for every package
- Seeds secrets from env vars via `RequiredSecrets()`
- Builds the tool group map from `GroupTools()` — no manual wiring in `toolgroup/group.go`
- The router calls `toolgroup.SetPackageTools()` at startup to merge package-declared tools into the group system

**See `tool/tfl/package.go` for a minimal example** (simple package with optional secret), or `tool/scheduletools/package.go` for a package with extra deps via `RegistrationContext.Extra`.

## State Management Rules

- **Helpers that mutate state must return errors** — never log-and-swallow. The *caller* decides whether to surface, log, or continue, but the error must never be invisible.
- **State machines must use explicit typed states** — not implicit map membership. Use enums for states, explicit `Start`/`Cancel`/`Complete` transitions, and a typed result struct for outcomes.
- **Prefer single source of truth** over overlapping tracking mechanisms. One `ChannelSet` instead of three separate maps tracking the same data.
- **Avoid forward-declared closures** — define functions before referencing them. If a closure needs to be passed to a constructor before it's defined, extract it into a method or use a callback interface.
- **Serialize read-modify-write cycles** — use a mutex when updating store-backed state to prevent TOCTOU races (see `RuntimeStateStore`, `config.Writer`).
- **Prefer explicit over implicit** — pass dependencies explicitly in params, not via closures capturing ambient state. Avoid default/inferred arguments.

## Channel Types

Channel types live in sub-packages under `channel/` and implement the `channelpkg.Package` interface (see `channel/channelpkg/channelpkg.go`). This mirrors the `toolpkg.Package` pattern for MCP tools — each channel type is a self-contained package with its own build logic, and the `channel/all/` registry aggregates them.

Adding a new channel type requires:

1. Create a new directory under `channel/` (e.g. `channel/slackchannel/`)
2. Implement the `Channel` interface in a transport file (e.g. `slack.go`)
3. Implement `channelpkg.Package`:
   - `Type()` — the `ChannelType` constant (e.g. `channel.TypeSlack`)
   - `Build(ctx, params)` — constructs the live `Channel` from config
   - `Provisioner()` — returns `EphemeralProvisioner` or nil
4. Define platform state types in the package using `channel.NewPlatformState()` / `channel.NewTeardownState()` with `json.RawMessage` data
5. Add the platform type constant in `channel/platform_state.go`: `PlatformSlack PlatformType = "slack"`
6. Add the channel type constant in `channel/channel.go`: `TypeSlack ChannelType = "slack"`
7. Add `SlackChannelConfig`-equivalent in `config/config.go`
8. Add the package to the registry in `channel/all/all.go`

**See `channel/socketchannel/` for a minimal example** (no provisioner), or `channel/telegramchannel/` for a package with provisioning and platform state.
