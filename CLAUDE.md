# tclaw

## Code Style
- Comment code that isn't obvious, prefer readability over clever code
- Errors must never be silently ignored — either log+skip if recoverable, or return them up the call stack
- Use proper typed consts and enums — never raw strings for known value sets (permission modes, event types, content block types, tools, etc.)
- This includes CLI flags that accept known values — model tool names, permission rules, etc. should all be typed
- Prefer param structs over multiple parameters for both inputs and outputs — keeps signatures clean and extensible

## Architecture
- Spawns the `claude` CLI binary directly — does NOT use `claude-agent-sdk-go` (it has bugs: stdin pipe never closed causing hangs, assistant message text not emitted as events)
- `agent/claude.go` — typed enums and event structs for the CLI's stream-json output
- `agent/agent.go` — core loop: reads from channel, spawns `claude --print --output-format stream-json`, parses response
- `channel/` — channel abstraction (unix socket, stdio)
- `cmd/chat/` — chat client that connects to the agent's unix socket
- Must unset `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` env vars before spawning claude subprocess (prevents nested session detection)

## Memory
- When I say "add to memory" or "remember this", update THIS file (CLAUDE.md), not the ~/.claude/ memory directory
