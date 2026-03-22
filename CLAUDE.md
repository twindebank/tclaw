# tclaw

## MANDATORY: Read Before Writing Any Code
**EVERY time you write or modify code — including in resumed/continued sessions — you MUST read @docs/go-patterns.md first.** No exceptions. This includes tests, one-line fixes, and refactors. Don't rely on memory or assumptions about conventions; read the file.


## TODO

### Features
- System message posted to admin channel on every redeployment (with commit hash / changelog)
- Google Maps / location tools — place search, directions, travel time; location history integration
- Periodic job to inspect logs automatically and open PRs to fix recurring errors
- Proper versioning for deployments (semver tags, changelog generation, release notes)
- Periodic job to audit repo dependencies and open upgrade PRs (needs `repo_*` + `dev_*` tools working together)
- Voice message transcription — Telegram sends voice notes as .ogg files which the agent can't read. Claude API has no native audio input (as of Mar 2026); use Groq Whisper API (generous free tier). Transcribe at ingest in the Telegram handler, inject as `[Voice message: "<transcript>"]` so the agent sees plain text with no changes needed downstream.
- Telegram Bot API integration — native tools for managing bot channels: get chat info, list members, pin messages, manage channel settings. Currently all Telegram management is done via BotFather manually; native tools would let the agent set up and configure channels programmatically.
- Token exhausted state — when Claude API limit is hit, record reset time in channel state. Scheduled jobs should be deferred until reset time (not silently dropped). On reset, prompt user whether to run deferred schedules or skip them. Normal inbound messages should be queued and replayed (or flagged as unactioned) when the limit resets so nothing is silently lost.
- Channel busy/free check — `channel_is_busy` tool: returns whether a channel has an active agent turn or recent conversation activity (with configurable timeout). Enables scheduled tasks to check before sending cross-channel messages, and to queue/defer if busy rather than interrupting.

### Maintenance
- Periodic jobs to check Claude Code changelog and dynamically update agent/CLI behavior
- Check CVEs and dynamically update dependencies
- Generally update dependencies (go mod tidy, bump versions)
- Other periodic maintenance tasks (e.g. rotate secrets, audit configs)

### Dev Experience
- GitHub Actions CI: run `go build ./...` + `go test ./...` on every PR (currently no CI)
- `dev_logs` time range filter — `since` param (e.g. "last 4 days") so historical logs are easily queryable
- Local dev parity — `make dev` that spins up the full stack with a test Telegram bot token and hot reload
- PR preview deployments — deploy PRs to a separate Fly.io app (`tclaw-preview`) for manual testing before merge
- `dev_status` should show CI check results alongside uncommitted changes
- Structured log viewer — `dev_logs` currently returns raw text; a mode that groups by session/tool-call would help debug agent turns
- Replay harness — record + replay Telegram message sequences to test agent behavior without live bots

## Code Style
- Comment code that isn't obvious, prefer readability over clever code
- Prefer inline docs on individual items over big block comments at the top of a group
- Errors must never be silently ignored — return errors up the call stack. Only log+swallow at the highest level (Run loop, Router) where recovery is clear.
- Never return data alongside an error — on error paths, return zero values for all non-error returns
- Use proper typed consts and enums — never raw strings for known value sets (permission modes, event types, content block types, tools, etc.)
- Prefer param structs over multiple parameters for both inputs and outputs
- **Prefer stateless functions over stateful structs** — exceptions: I/O resources and top-level orchestrators (Router) where someone must own goroutine lifecycles
- Use emojis in user-facing status messages (thinking, ready, tool use, etc.) for visual clarity
- Don't shorten/abbreviate names — use full words for packages, variables, functions (e.g. `connectiontools` not `connmgmt`)

## Architecture
- Spawns the `claude` CLI binary directly — does NOT use `claude-agent-sdk-go`
- `agent/` — stateless package. `agent.Run(ctx, opts)` is the entry point. `buildEnv()`/`buildArgs()` are pure functions.
- `channel/` — channel abstraction. `Channel` interface with `FanIn()` and `ChannelMap()` stateless helpers.
- `router/` — top-level orchestrator mapping users to agent goroutines. Only stateful struct.
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

| File | Owner | Purpose | Who reads it |
|------|-------|---------|-------------|
| `agent/system_prompt.md` | **Agent runtime behavior** — the single source of truth for how the agent operates. Tool usage, formatting rules, memory management, channel setup guidance, connection/schedule/dev workflow instructions. | Agent (all channels) |
| `CLAUDE.md` (this file) | **Developer instructions** — code style, architecture overview, build/deploy commands, doc guide. | Developer (us), agent in dev worktree |
| `docs/features.md` | **Developer feature reference** — what tclaw does and how it's configured. Implementation details, config examples, security model. Does NOT duplicate agent behavior docs. | Developer (us), agent in dev worktree |
| `docs/architecture.md` | **Technical internals** — package map, dependency layers, data flows, security boundaries, directory layout. | Developer (us), agent in dev worktree |
| `docs/deployment.md` | **Operations** — Fly.io deployment, secrets, commands, first-time setup, CI. | Developer/operator |
| `docs/go-patterns.md` | **Code conventions** — comments, error handling, testing, function design, naming. | Developer (us), agent in dev worktree |
| `agent.DefaultMemoryTemplate` | **User memory seed** — minimal scaffolding for the agent's per-user CLAUDE.md. NOT project docs. | Agent (loaded automatically each session) |

### Key rule: no duplication between system prompt and docs/

`agent/system_prompt.md` defines agent behavior. `docs/features.md` describes how tclaw works for developers. These serve different audiences and should NOT contain the same content. If you're documenting how the agent should use a tool → system prompt. If you're documenting how a feature is implemented or configured → features.md.

## Reference Docs
- @docs/go-patterns.md — comments, error handling, testing, function design, naming
- @docs/deployment.md — Fly.io deployment, secrets, commands, first-time setup, CI
- @docs/features.md — feature reference for developers (config, implementation, security)
- @docs/architecture.md — package map, dependency layers, data flows, auth flows, directory layout, secret management, environments

### Keeping Docs Up to Date
- **When adding or changing agent-facing behavior** (tool usage, formatting, memory rules) — update `agent/system_prompt.md`
- **When adding or changing a feature** (implementation, config, wiring) — update @docs/features.md
- **When changing architecture** (new packages, data flows, auth, directory layout) — update @docs/architecture.md
- **When adding new MCP tools** — update `agent/system_prompt.md` (how the agent uses them) AND @docs/features.md (how they're implemented/configured)
- **When changing deployment/config** — update @docs/architecture.md and @docs/deployment.md
- **When adding a new channel type** — update @docs/features.md, @docs/architecture.md, and `agent/system_prompt.md`
- **When changing Go conventions** — update @docs/go-patterns.md

## Fly.io Operations
- **Use `go run . deploy logs`** (or `go run . logs`) to view recent production logs — shows last 100 lines by default, use `-n N` to change, `-f` to stream.
- `fly deploy --local-only --no-cache -a tclaw` to force a clean Docker rebuild (avoids stale cache issues).
- Use `go run . deploy` for the standard deploy flow, but be aware it doesn't pass `--no-cache` by default.

## Related Projects
- **nanoclaw** — similar project (TypeScript, Docker containers, Anthropic Agent SDK). Repo: `https://github.com/qwibitai/nanoclaw`. Clone to `/tmp/nanoclaw` when asked about it.

## Memory
- When I say "add to memory" or "remember this", update THIS file (CLAUDE.md), not the ~/.claude/ memory directory
- **NEVER use project-level memory** (`~/.claude/projects/.../memory/`) — all memory goes in THIS file
- Never deploy (`go run . deploy` or any deploy command) without the user explicitly asking to deploy. Committing code does not imply permission to deploy.
