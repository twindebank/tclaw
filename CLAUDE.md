# tclaw

## Git Workflow
- **NEVER use Graphite (`gt`) in this repo** — use plain `git` and `gh` only
- **SSH key is in 1Password** — if `git push` fails with SSH auth errors, just wait and retry. Don't try workarounds (HTTPS, config changes). The user needs to be present to unlock.
- **PR creation**: `git push -u origin HEAD` then `gh pr create`

## MANDATORY: Read Before Writing Any Code
**EVERY time you write or modify code — including in resumed/continued sessions — you MUST read @docs/go-patterns.md first AND follow every pattern exactly.** No exceptions. This includes tests, one-line fixes, and refactors. Don't rely on memory or assumptions about conventions; read the file and match its patterns precisely. Tests MUST use `t.Run` subtests grouped under one top-level func per method — never split scenarios into separate top-level test functions.


## Code Style
- **Fail early, fail at build time** — if something is required at runtime, validate it as early as possible. Prefer build-time checks (Dockerfile `RUN test`, compile-time assertions) over runtime errors. Never deploy a binary that's known to be broken.
- Comment code that isn't obvious, prefer readability over clever code
- Prefer inline docs on individual items over big block comments at the top of a group
- Errors must never be silently ignored — return errors up the call stack. Only log+swallow at the highest level (Run loop, Router) where recovery is clear.
- Never return data alongside an error — on error paths, return zero values for all non-error returns
- Use proper typed consts and enums — never raw strings for known value sets (permission modes, event types, content block types, tools, etc.)
- Prefer param structs over multiple parameters for both inputs and outputs
- **Prefer stateless functions over stateful structs** — exceptions: I/O resources and top-level orchestrators (Router) where someone must own goroutine lifecycles
- Use emojis in user-facing status messages (thinking, ready, tool use, etc.) for visual clarity
- Don't shorten/abbreviate names — use full words for packages, variables, functions (e.g. `credentialtools` not `credmgmt`)

## Architecture
- Spawns the `claude` CLI binary directly — does NOT use `claude-agent-sdk-go`
- All packages live under `internal/` — see each package's doc comment for its responsibility
- `internal/agent/` — stateless agent loop. `agent.Run(ctx, opts)` spawns CLI, streams responses.
- `internal/channel/` — transport abstraction. `Channel` interface, `FanIn()`, `RuntimeStateStore`. Types in sub-packages (`socketchannel/`, `stdiochannel/`, `telegramchannel/`).
- `internal/config/` — YAML config loading + `config.Writer` for atomic mutations.
- `internal/router/` — top-level orchestrator. Per-user lifecycle, channel building, MCP servers. Only stateful struct.
- `internal/tool/` — MCP tool packages. Each is self-contained with `toolpkg.Package` interface.
- Per-user isolation via `HOME` env var on claude subprocess — all CLI state scoped per user

### Directory model
1. **Agent memory** (`<user>/memory/`) — agent reads/writes freely, sandboxed via CWD + `--add-dir`
2. **Claude Code state** (`<user>/home/.claude/`) — internal CLI state, off limits to agent. Symlink bridges CLAUDE.md.
3. **tclaw state** (`<user>/state/`, `sessions/`, `secrets/`) — not mounted in sandbox, MCP tool access only
4. **MCP config** (`<user>/mcp-config/`) — mounted read-only in sandbox for `--mcp-config`

## Documentation Guide

Documentation lives close to the code. Three layers, strictly separated:

1. **Tool descriptions** (`definitions.go`, inline defs) — parameters, usage, credential keys, setup flows. The agent reads these at runtime. Single source of truth for individual tools.
2. **System prompt** (`internal/agent/system_prompt.md`) — agent identity, behavioral rules, cross-cutting constraints. No tool-specific parameter details.
3. **Developer docs** — this CLAUDE.md, package doc comments, and the files below.

### Reference Docs
- @docs/go-patterns.md — comments, error handling, testing, function design, naming
- @docs/architecture.md — high-level overview: dependency layers, security model
- @docs/deployment.md — Fly.io deployment, secrets, commands, first-time setup, CI

### Keeping Docs Up to Date
- **New MCP tools** → write tool descriptions in `definitions.go`. Add to system prompt only for cross-cutting behavioral rules.
- **New packages** → add package doc comment on the primary `.go` file.
- **Agent behavior changes** → update `internal/agent/system_prompt.md`
- **Deployment/config changes** → update @docs/deployment.md
- **Go conventions** → update @docs/go-patterns.md

## Deployment
- **Deploys happen automatically via GitHub Actions CI** on push to main (`.github/workflows/deploy.yml`)
- CI builds locally on the GitHub runner (7GB RAM) and pushes to Fly — avoids the remote builder OOM from gotd/td
- `tclaw.yaml` is gitignored. The `TCLAW_YAML` GitHub secret holds a seed copy for first boot only — it never overwrites the live config on the persistent volume.
- **NEVER commit or `git add` tclaw.yaml** — it contains `${secret:...}` refs and environment-specific config.
- `fly.toml` is also gitignored — copy `fly.example.toml` and update the app name, or run `tclaw init`.
- `tclaw config push` syncs local config to the remote volume (live) AND updates the seed secret.
- **Local deploys** still work: `go run . deploy` builds with Docker and deploys via `fly deploy --local-only`
- **The `deploy` MCP tool is status-only** — it checks what's deployed vs main, does NOT deploy. Deploys are CI's job.
- **Config sync**: `tclaw config push` pushes local config to remote Fly volume. `tclaw config pull` pulls remote to local. `tclaw config diff` shows differences.
- **Logs**: `go run . deploy logs` (or `go run . logs`) to view recent production logs
- Never deploy (`go run . deploy` or any deploy command) without the user explicitly asking to deploy. Committing code does not imply permission to deploy.

## Related Projects
- **nanoclaw** — similar project (TypeScript, Docker containers, Anthropic Agent SDK). Repo: `https://github.com/qwibitai/nanoclaw`. Clone to `/tmp/nanoclaw` when asked about it.

## Memory
- When I say "add to memory" or "remember this", update THIS file (CLAUDE.md), not the ~/.claude/ memory directory
- **NEVER use project-level memory** (`~/.claude/projects/.../memory/`) — all memory goes in THIS file
- Never deploy (`go run . deploy` or any deploy command) without the user explicitly asking to deploy. Committing code does not imply permission to deploy.
