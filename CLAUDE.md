# tclaw

## Code Style
- Comment code that isn't obvious, prefer readability over clever code
- Prefer inline docs on individual items over big block comments at the top of a group — each const/var/field should carry its own explanation
- Errors must never be silently ignored — return errors up the call stack. Only log+swallow at the highest level (Run loop, Router) where recovery is clear. Low-level helpers must return errors, not swallow them.
- Never return data alongside an error — on error paths, return zero values for all non-error returns. The caller should not trust data when err != nil.
- Use proper typed consts and enums — never raw strings for known value sets (permission modes, event types, content block types, tools, etc.)
- This includes CLI flags that accept known values — model tool names, permission rules, etc. should all be typed
- Prefer param structs over multiple parameters for both inputs and outputs — keeps signatures clean and extensible
- **Prefer stateless functions over stateful structs** — avoid structs with methods that mutate shared state. Caller should own state and pass it to pure/stateless functions that return new values. Exceptions: I/O resources (net.Conn, os.File) and top-level orchestrators (Router) where someone must own goroutine lifecycles.
- Use emojis in user-facing status messages (thinking, ready, tool use, etc.) for visual clarity

## Architecture
- Spawns the `claude` CLI binary directly — does NOT use `claude-agent-sdk-go` (it has bugs: stdin pipe never closed causing hangs, assistant message text not emitted as events)
- `agent/` — stateless package, no Agent struct. `agent.Run(ctx, opts)` is the entry point. `handle()` takes session ID in and returns it out. `buildEnv()`/`buildArgs()` are pure functions.
- `agent/claude.go` — typed enums and event structs for the CLI's stream-json output
- `channel/` — channel abstraction (unix socket, stdio). `Channel` interface with `Info()` for identity/type. `FanIn()` and `ChannelMap()` are stateless helpers. Each channel reports its own ID and type.
- `router/` — top-level orchestrator mapping users to agent goroutines. Owns goroutine lifecycles (cancel, wait). Only stateful struct since it manages concurrency.
- `user/` — `user.ID` and `user.Config` types
- `cmd/chat/` — TUI chat client that connects to the agent's unix socket
- Per-user isolation via `HOME` env var on claude subprocess — all CLI state (`~/.claude/`) scoped per user
- `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` stripped from subprocess env in `buildEnv()` (prevents nested session detection)

## Memory
- When I say "add to memory" or "remember this", update THIS file (CLAUDE.md), not the ~/.claude/ memory directory
