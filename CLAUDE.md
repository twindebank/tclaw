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
- `agent/` — stateless package. `agent.Run(ctx, opts)` is the entry point. `buildEnv()`/`buildArgs()` are pure functions.
- `channel/` — channel abstraction. Core `Channel` interface, `RuntimeStateStore`, `PlatformState`/`TeardownState` (extensible via `json.RawMessage`), `FanIn()` and `ChannelMap()` helpers. Channel types live in sub-packages (`socketchannel/`, `stdiochannel/`, `telegramchannel/`) with a registry at `channel/all/`.
- `channel/channelpkg/` — `ChannelPackage` interface and `Registry` for channel type registration (mirrors `tool/toolpkg/`).
- `config/` — YAML config loading + `config.Writer` for atomic config mutations. All channels live in `tclaw.yaml`.
- `reconciler/` — desired-state reconciliation. Compares config channels against runtime state, auto-provisions when possible.
- `router/` — top-level orchestrator mapping users to agent goroutines. Uses `channelpkg.Registry` for channel building. Only stateful struct.
- Per-user isolation via `HOME` env var on claude subprocess — all CLI state scoped per user

### Directory model
1. **Agent memory** (`<user>/memory/`) — agent reads/writes freely, sandboxed via CWD + `--add-dir`
2. **Claude Code state** (`<user>/home/.claude/`) — internal CLI state, off limits to agent. Symlink bridges CLAUDE.md.
3. **tclaw state** (`<user>/state/`, `sessions/`, `secrets/`) — not mounted in sandbox, MCP tool access only
4. **MCP config** (`<user>/mcp-config/`) — mounted read-only in sandbox for `--mcp-config`

## Documentation Guide

This project has several documentation files serving different audiences. Understanding who reads what prevents duplication and keeps things in sync.

### Audiences

| Audience | Context | Primary docs |
|----------|---------|-------------|
| **Developer** (us, working on the repo) | Coding at repo root with full source access | This CLAUDE.md, @docs/go-patterns.md, @docs/architecture.md, @docs/features.md, @docs/deployment.md |
| **Agent in assistant channel** | Runtime, no code access, restricted tools | `agent/system_prompt.md` (injected as system prompt) + user's memory CLAUDE.md |
| **Agent in developer channel** | Runtime, with a cloned worktree via dev_start | `agent/system_prompt.md` + this CLAUDE.md and @docs/ (read from the worktree before making changes) |

### What goes where

| File | Purpose | Who reads it |
|------|---------|-------------|
| Tool descriptions (`definitions.go`, inline defs) | **Tool-specific reference** — parameters, usage, credential keys, setup flows. The single source of truth for individual tools. | Agent (at runtime) |
| `agent/system_prompt.md` | **Agent runtime behavior** — identity, formatting, cross-cutting rules, multi-tool flows. No tool-specific parameter details. | Agent (all channels) |
| `CLAUDE.md` (this file) | **Developer instructions** — code style, architecture overview, build/deploy commands, doc guide. | Developer (us), agent in dev worktree |
| `docs/features.md` | **Developer feature summary** — concise overview of what tclaw does. Points to tool descriptions and architecture.md for details. | Developer (us), agent in dev worktree |
| `docs/architecture.md` | **Technical internals** — package map, dependency layers, data flows, security boundaries, directory layout, secret management, config. | Developer (us), agent in dev worktree |
| `docs/deployment.md` | **Operations** — Fly.io deployment, secrets, commands, first-time setup, CI. | Developer/operator |
| `docs/go-patterns.md` | **Code conventions** — comments, error handling, testing, function design, naming. | Developer (us), agent in dev worktree |
| `agent.DefaultMemoryTemplate` | **User memory seed** — minimal scaffolding for the agent's per-user CLAUDE.md. NOT project docs. | Agent (loaded automatically each session) |

### Key rule: tool descriptions are the single source of truth

MCP tool descriptions (`definitions.go` and inline tool defs) are the **single source of truth** for tool parameters, usage, and behavior. The agent reads these directly at runtime. Three documentation layers, strictly separated:

1. **Tool descriptions** — parameters, usage, credential keys, setup flows. The agent reads these. This is where tool-specific details go — NOT the system prompt, NOT features.md.
2. **System prompt** (`agent/system_prompt.md`) — agent identity, behavioral rules, cross-cutting constraints that span multiple tools (e.g. "never ask for secrets in chat"), and multi-tool flow guidance. No tool-specific parameter details.
3. **Developer docs** (`docs/features.md`, `docs/architecture.md`) — developer-facing summaries, config examples, security model, package structure. Not read by the agent at runtime.

Do NOT duplicate information across these layers. If you're documenting how a specific tool works → tool description. If you're documenting cross-cutting agent behavior → system prompt. If you're documenting implementation/config for developers → features.md or architecture.md.

## Reference Docs
- @docs/go-patterns.md — comments, error handling, testing, function design, naming
- @docs/deployment.md — Fly.io deployment, secrets, commands, first-time setup, CI
- @docs/features.md — feature reference for developers (config, implementation, security)
- @docs/architecture.md — package map, dependency layers, data flows, auth flows, directory layout, secret management, environments

### Keeping Docs Up to Date
- **When adding new MCP tools** — write detailed tool descriptions in `definitions.go` or inline tool defs. Include: parameters, usage, credential/secret store key names, setup flows. Only add to `agent/system_prompt.md` if the tools need cross-cutting behavioral rules or multi-tool flow guidance. Update @docs/architecture.md package map and secret store keys.
- **When adding or changing agent-facing behavior** (formatting, memory rules, cross-tool constraints) — update `agent/system_prompt.md`
- **When adding or changing a feature** (implementation, config, wiring) — update @docs/features.md
- **When changing architecture** (new packages, data flows, auth, directory layout) — update @docs/architecture.md
- **When changing deployment/config** — update @docs/architecture.md and @docs/deployment.md
- **When adding a new channel type** — update @docs/features.md, @docs/architecture.md, and `agent/system_prompt.md`
- **When changing Go conventions** — update @docs/go-patterns.md

## Deployment
- **Deploys happen automatically via GitHub Actions CI** on push to main (`.github/workflows/deploy.yml`)
- CI builds locally on the GitHub runner (7GB RAM) and pushes to Fly — avoids the remote builder OOM from gotd/td
- `tclaw.yaml` is gitignored. The `TCLAW_YAML` GitHub secret holds a seed copy for first boot only — it never overwrites the live config on the persistent volume.
- **NEVER commit or `git add` tclaw.yaml** — it contains `${secret:...}` refs and environment-specific config.
- `tclaw config push` syncs local config to the remote volume (live) AND updates the seed secret. This is the primary way to update deployed config.
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
