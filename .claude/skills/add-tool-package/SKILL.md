---
name: add-tool-package
description: Scaffold a new MCP tool package with registration, definitions, and tests
allowed-tools: Bash, Read, Edit, Write, Glob, Grep
---

Scaffold a new MCP tool package under `internal/tool/`. Ask the user for:

1. **Package name** — lowercase, no abbreviations (e.g. `weathertools`, `spotify`)
2. **Brief description** — what the tools do (e.g. "Weather forecasts and alerts")
3. **Tool group** — which group these belong to, or a new one (ask if unsure)
4. **Needs credentials?** — API key, OAuth, or none

## Reference patterns

Before writing any code, read these files to match the exact patterns:

- `internal/tool/tfl/package.go` — minimal package (optional secret, no OAuth)
- `internal/tool/scheduletools/package.go` — package with extra dependencies
- `internal/tool/toolpkg/toolpkg.go` — the `Package` interface
- `internal/tool/all/all.go` — where packages are registered
- `internal/tool/tfl/definitions.go` — tool definition pattern
- `internal/toolgroup/group.go` — tool group constants

Also read `docs/go-patterns.md` for code style rules.

## Files to create

For a package named `{name}`:

### `internal/tool/{name}/package.go`
- Struct implementing `toolpkg.Package`
- `Name()` returns stable identifier
- `Description()` returns human-readable summary
- `Group()` returns the tool group
- `GroupTools()` maps group to `[]claudecli.Tool` with glob pattern `"mcp__tclaw__{name}_*"`
- `RequiredSecrets()` — empty slice if no credentials, or `[]SecretSpec{...}` with env prefix
- `Info()` returns `*toolpkg.PackageInfo` with credential status
- `Register()` calls `RegisterTools(handler, deps)`
- If credentials needed: implement `CredentialProvider` interface (`CredentialSpec()`, `OnCredentialSetChange()`)

### `internal/tool/{name}/definitions.go`
- Tool name constants: `toolNameFoo = "{name}_foo"`
- `Deps` struct with required dependencies
- `RegisterTools(handler *mcp.Handler, deps Deps) error` function
- MCP tool definitions with `mcp.ToolDef{}` — name, description, parameters
- Handler functions for each tool

### `internal/tool/{name}/{name}_test.go`
- Package name: `{name}_test` (external test package)
- `setup(t)` helper that creates `mcp.NewHandler()` and calls `RegisterTools()`
- `callTool(t, h, name, args)` and `callToolExpectError(t, h, name, args)` helpers
- Tests for success cases and input validation
- Use `require` from testify, `t.Run` subtests, `// --- helpers ---` separator

## Registration

After creating the package files, add it to `internal/tool/all/all.go`:

1. Add the import
2. Add any new fields to the `Params` struct if the package needs extra dependencies
3. Instantiate the package in the `packages` slice in `NewRegistry()`

## Verification

Run `go build ./...` to verify compilation. Run `go test ./internal/tool/{name}/...` to verify tests pass.

## Do NOT:
- Create separate `register.go` or `handler.go` files — keep it to `package.go`, `definitions.go`, and the test file
- Add the tool group to a hardcoded list — `GroupTools()` handles registration automatically
- Abbreviate package or variable names
