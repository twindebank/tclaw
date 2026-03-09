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
- Don't shorten/abbreviate names — use full words for packages, variables, functions, etc. (e.g. `connectiontools` not `connmgmt`)

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

### Three-zone directory model
Per-user data is split into three zones with clear access boundaries:
1. **Agent memory** (`<user>/memory/`) — agent reads/writes freely, sandboxed via CWD + `--add-dir`. Contains CLAUDE.md (real file) and topic subfiles.
2. **Claude Code state** (`<user>/home/.claude/`) — internal CLI state (conversation history, settings, plans). Off limits to the agent. A symlink at `home/.claude/CLAUDE.md` → `../../memory/CLAUDE.md` bridges the CLI's auto-load with the agent's sandbox.
3. **tclaw state** (`<user>/state/`, `sessions/`, `secrets/`, `runtime/`) — accessible only via MCP tools, outside the agent's HOME entirely.

## Deployment (Fly.io)

### Overview
- Hosted on Fly.io in `lhr` (London), app name: `tclaw`
- Local Docker builds only (`make deploy`) — no auto-deploy on push
- Persistent volume `tclaw_data` at `/data` for per-user state
- Health check at `/healthz` on port 9876
- Config baked into image at `/etc/tclaw/tclaw.yaml` from `tclaw.deploy.yaml`

### Secret management
- Secrets stored locally in OS keychain via `make secret ARGS="set NAME value"`
- `make deploy-secrets` scans `tclaw.deploy.yaml` for `${secret:NAME}` refs, reads each from keychain, pushes to Fly in one call
- At runtime: Fly injects secrets as env vars → config resolves them → `main.go` scrubs env vars before spawning Claude subprocesses

### Commands
```
make deploy-secrets    # Push keychain secrets to Fly
make deploy            # Build locally + deploy to Fly
fly status -a ${{ vars.FLY_APP_NAME }}    # Check app status
fly logs -a ${{ vars.FLY_APP_NAME }}      # Tail logs
fly scale count 0 -a ${{ vars.FLY_APP_NAME }} --yes   # Spin down
fly scale count 1 -a ${{ vars.FLY_APP_NAME }} --yes   # Spin up
```

### First-time setup
1. `brew install flyctl && fly auth login`
2. `fly apps create tclaw`
3. `fly volumes create tclaw_data --region lhr --size 1 -a ${{ vars.FLY_APP_NAME }} -y`
4. Set secrets in keychain, then `make deploy-secrets`
5. `make deploy`

### OAuth callback URL
`https://your-app.fly.dev/oauth/callback` — set this as the redirect URI in Google OAuth console.

### CI (optional)
GitHub Actions workflow at `.github/workflows/deploy.yml` — manual trigger only (`workflow_dispatch`). Needs `FLY_API_TOKEN` GitHub secret from `fly tokens create deploy -x 999999h`.

## Memory
- When I say "add to memory" or "remember this", update THIS file (CLAUDE.md), not the ~/.claude/ memory directory
