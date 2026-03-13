# tclaw

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

## Reference Docs
- @docs/go-patterns.md — comments, error handling, testing, function design, naming
- @docs/deployment.md — Fly.io deployment, secrets, commands, first-time setup, CI
- @docs/features.md — complete feature documentation (channels, memory, auth, connections, scheduling, MCP tools)
- @docs/architecture.md — package map, dependency layers, data flows, auth flows, directory layout, secret management, environments

### Keeping Docs Up to Date
- **When adding or changing a feature** — update @docs/features.md
- **When changing architecture** (new packages, data flows, auth, directory layout) — update @docs/architecture.md
- **When adding new MCP tools** — add to the relevant section in @docs/features.md
- **When changing deployment/config** — update @docs/architecture.md and @docs/deployment.md
- **When adding a new channel type** — update @docs/features.md and @docs/architecture.md
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
