# tclaw Architecture

tclaw spawns isolated `claude` CLI subprocesses — one per user — and manages communication through multiple transport channels. It does **not** use the Claude Agent SDK; it drives the CLI binary directly via `--output-format stream-json`.

```
  Channels (socket, Telegram, stdio)
            │
       ┌────▼─────┐
       │  Router   │  per-user lifecycle, lazy start/stop
       └────┬──────┘
            │
  ┌─────────▼──────────┐
  │  claude CLI process │  spawned per turn, stream-json output
  └─────────┬──────────┘
            │
  ┌─────────▼──────────┐
  │  MCP Server        │  per-user, localhost:<random>, bearer token
  └────────────────────┘
```

All packages live under `internal/`. See each package's doc comment for its responsibility.

## Dependency Layers

Dependencies flow strictly downward — no circular imports.

```
Layer 1:  Pure types (user, claudecli, store.Store, secret.Store)
Layer 2:  Domain models (credential, schedule, channel.Channel)
Layer 3:  Managers (credential.Manager, schedule.Store, channel.RuntimeStateStore)
Layer 4:  Stateless handlers (oauth, mcp.Handler, mcp/discovery)
Layer 5:  Channel implementations (socketchannel, stdiochannel, telegramchannel)
Layer 6:  Agent loop (agent.Run — spawns CLI, handles auth, manages turns)
Layer 7:  HTTP server (oauth.CallbackServer — callbacks, webhooks, health)
Layer 8:  Tool implementations (channeltools, credentialtools, google, etc.)
Layer 9:  Configuration (config — YAML parsing, secret resolution)
Layer 10: CLI dispatch (cli/ — subcommand routing)
Layer 11: Orchestration (router, main)
```

## Security Model

Four boundaries protect user data and the host system:

### 1. Subprocess Isolation
- **Environment allowlist** — only safe env vars (PATH, TERM, LANG, etc.) reach the subprocess. Cloud credentials, SSH agents, GitHub tokens, and tclaw internals are excluded. See `agent/handle.go:allowedEnvPrefixes`.
- **Filesystem sandbox** (Linux only) — bubblewrap mount namespace isolates each user's subprocess. Only their own memory/home dirs are writable; system paths are read-only; other users' data is invisible. See `agent/sandbox.go`.

### 2. Channel Boundary
- Socket and stdio channels are blocked in non-local environments (no authentication).
- Telegram restricts access via user-level `telegram.user_id` — messages from other users are dropped.

### 3. MCP Tool Boundary
- Per-user MCP server on localhost with random bearer token.
- 1 MiB request body limit, audit logging, permission-gated via `tool_groups`.

### 4. Secret Boundary
- NaCl secretbox encryption with per-user HKDF-derived keys. See `libraries/secret/`.
- Config secrets via `${secret:NAME}` (keychain → env var fallback, then scrubbed).
- Runtime secrets via encrypted store (agent-collected OAuth tokens, API keys).
- Fly secrets seeded into encrypted store on boot, then scrubbed from env.
