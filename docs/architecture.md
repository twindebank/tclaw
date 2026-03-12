# tclaw Architecture

## Overview

tclaw spawns isolated `claude` CLI subprocesses — one per user — and manages communication through multiple transport channels. It does **not** use the Claude Agent SDK; it drives the CLI binary directly via `--output-format stream-json`.

```
┌─────────────────────────────────────────────────────────────┐
│  Channels                                                    │
│  ┌────────┐  ┌───────┐  ┌──────────┐  ┌──────────────┐     │
│  │ Socket │  │ Stdio │  │ Telegram │  │ Schedule Msg │     │
│  └───┬────┘  └───┬───┘  └────┬─────┘  └──────┬───────┘     │
│      └───────────┴───────────┴───────────────┘              │
│                         │                                    │
│                    FanIn()                                    │
│                         │                                    │
│              ┌──────────▼───────────┐                        │
│              │  agent.RunWithMessages│                        │
│              │  (main event loop)    │                        │
│              └──────────┬───────────┘                        │
│                         │                                    │
│              ┌──────────▼───────────┐                        │
│              │  claude CLI subprocess│                        │
│              │  --output-format      │                        │
│              │    stream-json        │                        │
│              └──────────┬───────────┘                        │
│                         │                                    │
│              ┌──────────▼───────────┐                        │
│              │  MCP Server (per-user)│                        │
│              │  localhost:<random>    │                        │
│              └──────────────────────┘                        │
└─────────────────────────────────────────────────────────────┘
```

## Package Map

### Core

| Package | Responsibility |
|---------|----------------|
| `main.go` | Entry point: dispatches to `cli.Run()` |
| `cli/` | CLI subcommand dispatch. `serve` (start server), `chat` (TUI client), `secret` (keychain management), `deploy` (Fly.io deployment, secrets, suspend/resume). |
| `router/` | Per-user agent lifecycle management. Owns goroutine lifetimes, directory setup, MCP server creation, tool registration. The only stateful orchestrator. |
| `agent/` | Stateless package. `Run(ctx, opts)` reads messages from channels, handles auth flows, spawns CLI subprocess per turn, streams responses back. |
| `channel/` | Transport abstraction. `Channel` interface with implementations: Socket, Stdio, Telegram, Dynamic. `FanIn()` multiplexer and `ChannelMap()` helper. |
| `config/` | YAML parsing, secret resolution, config validation. |

### Auth & Connections

| Package | Responsibility |
|---------|----------------|
| `oauth/` | Stateless OAuth 2.0 helpers (`BuildAuthURL`, `ExchangeCode`, `RefreshToken`). `CallbackServer` handles HTTP endpoints for OAuth callbacks, Telegram webhooks, and health checks. |
| `provider/` | OAuth provider registry. Stateless lookup by provider ID. Currently: Google. |
| `connection/` | Connection CRUD and credential management. Bridges `store.Store` (connection metadata) and `secret.Store` (encrypted credentials). Also manages remote MCP server configs. |

### Tools (MCP)

| Package | Responsibility |
|---------|----------------|
| `mcp/` | JSON-RPC tool registry (`Handler`), HTTP server (`Server`), config file generation (`GenerateConfigFile`). |
| `mcp/discovery/` | OAuth discovery for remote MCP servers (RFC 7591 dynamic registration). Safe HTTP client that blocks private IPs and requires HTTPS. |
| `tool/channeltools/` | MCP tools for dynamic channel management (create, list, edit, delete). |
| `tool/connectiontools/` | MCP tools for OAuth connection management (add, remove, list, auth_wait). |
| `tool/remotemcp/` | MCP tools for remote MCP server management (add, remove, list, auth_wait). |
| `tool/scheduletools/` | MCP tools for cron schedule management (create, list, edit, delete, pause, resume). |
| `tool/google/` | Google Workspace tools registered when a Google connection exists. Delegates to `gws` binary. |

### Infrastructure

| Package | Responsibility |
|---------|----------------|
| `libraries/store/` | Key-value `Store` interface with filesystem-backed implementation (`NewFS`). JSON serialization to disk. |
| `libraries/secret/` | Encrypted secret storage. `Store` interface with two implementations: `EncryptedStore` (NaCl secretbox, for deployed) and `KeychainStore` (macOS Keychain, for local dev). `Resolve()` picks the right one. |
| `libraries/id/` | TypeID generation (ULID-based). Used for schedule IDs. |
| `claudecli/` | Typed enums and event structs for the Claude CLI's stream-json output. Models, permission modes, tools, content block types. Pure data types, no I/O. |
| `user/` | `user.ID` and `user.Config` types. Pure data, no I/O. |
| `schedule/` | Cron schedule store and scheduler daemon. The scheduler runs at user lifetime and injects messages into channels when schedules fire. |

### CLI Tools

| Package | Responsibility |
|---------|----------------|
| `cmd/chat/` | Bubbletea TUI client (separate Go module). Connects to the agent via unix socket. Invoked via `tclaw chat`. |

## Dependency Layers

Dependencies flow strictly downward — no circular imports.

```
Layer 1:  Pure types (user, claudecli, store.Store interface, secret.Store interface)
Layer 2:  Domain models (connection.Connection, schedule.Schedule, channel.Channel interface)
Layer 3:  Managers (connection.Manager, schedule.Store, channel.DynamicStore)
Layer 4:  Stateless handlers (oauth, mcp.Handler, mcp/discovery)
Layer 5:  Channel implementations (socket, stdio, telegram, dynamic)
Layer 6:  Agent loop (agent.Run — spawns CLI, handles auth, manages turns)
Layer 7:  HTTP server (oauth.CallbackServer — callbacks, webhooks, health)
Layer 8:  Tool implementations (channeltools, connectiontools, remotemcp, scheduletools, google)
Layer 9:  Configuration (YAML parsing, secret resolution)
Layer 10: CLI dispatch (cli/ — subcommand routing, deploy/secret commands)
Layer 11: Orchestration (router, main)
```

## Data Flow

### Message Lifecycle

1. User sends a message via a channel (socket, Telegram, etc.)
2. `channel.FanIn()` multiplexes all channels into a single `<-chan TaggedMessage`
3. Router's `waitAndStart()` receives the first message and starts the agent
4. `agent.RunWithMessages()` processes messages in a loop:
   - Control commands (`stop`, `reset`, `login`, `auth`) are handled inline
   - Auth flow messages are routed to the per-channel auth state machine
   - Regular messages spawn a CLI subprocess via `handle()`
5. `handle()` builds CLI args, starts `claude` with stream-json output
6. `streamResponse()` parses JSON events and writes to the channel via `turnWriter`
7. The channel's `Send()`/`Edit()` methods deliver output to the user
8. `Done()` signals end of turn

### Auth Flow

```
User sends message
    │
    ▼
CLI returns authentication_failed
    │
    ▼
Agent starts auth flow (per-channel state machine)
    │
    ├─► OAuth: launch `claude setup-token` in goroutine
    │   └─► Browser opens, user consents
    │       └─► Long-lived setup token captured from stdout
    │           └─► Ask user: deploy to prod?
    │               └─► If yes: `fly secrets set CLAUDE_SETUP_TOKEN_<USER>=<token>`
    │
    └─► API Key: prompt user, validate prefix, encrypt and store
        │
        ▼
    Retry original message
```

### OAuth Connection Flow

```
Agent calls connection_add tool
    │
    ▼
MCP handler generates OAuth state, registers pending flow on CallbackServer
    │
    ▼
Returns auth URL to agent → agent sends to user
    │
    ▼
User clicks URL → browser → provider consent
    │
    ▼
Provider redirects to /oauth/callback?code=X&state=Y
    │
    ▼
CallbackServer validates state, exchanges code for tokens
    │
    ▼
Stores connection + encrypted credentials
    │
    ▼
Agent calls connection_auth_wait → polls until complete
    │
    ▼
Provider-specific tools registered (e.g. google_workspace)
```

### Remote MCP Flow

```
Agent calls remote_mcp_add(name, url)
    │
    ▼
Discovery client fetches /.well-known/oauth-authorization-server
    │
    ▼
If auth required: dynamically registers client (RFC 7591)
    │
    ▼
Stores remote MCP config + regenerates mcp-config.json
    │
    ▼
Claude CLI picks up new MCP on next turn (reads --mcp-config)
```

## Per-User Directory Layout

```
<base_dir>/
  <user-id>/
    home/                      HOME env var for Claude subprocess
      .claude/                 Claude Code internal state
        CLAUDE.md              symlink → ../../memory/CLAUDE.md
        projects/              conversation history
        settings.json          CLI settings
      Library/
        Keychains              symlink → real macOS Keychains
    memory/                    agent's sandbox (CWD + --add-dir)
      CLAUDE.md                real file, agent's persistent memory
      *.md                     topic files
    state/                     tclaw persistent data (JSON files)
    sessions/                  Claude CLI session IDs per channel
    secrets/                   NaCl-encrypted credentials
    runtime/                   ephemeral files (mcp-config.json)
    main.sock                  unix socket for "main" channel
    *.sock                     unix sockets for other channels
```

## Secret Management Architecture

### Three Resolution Layers

```
┌─────────────────────────────────────┐
│  Config: ${secret:NAME}             │
│                                     │
│  1. Try OS keychain (local only)    │
│  2. Try environment variable        │
│  3. Error if not found              │
│                                     │
│  After resolution: unset env var    │
└─────────────────────────────────────┘

┌─────────────────────────────────────┐
│  Runtime: secret.Store interface    │
│                                     │
│  Local: KeychainStore (macOS)       │
│  Deployed: EncryptedStore (NaCl)    │
│    - Master key: TCLAW_SECRET_KEY   │
│    - Per-user key: HKDF(master,uid) │
│    - Files: <user>/secrets/*.enc    │
└─────────────────────────────────────┘

┌─────────────────────────────────────┐
│  Subprocess isolation               │
│                                     │
│  Stripped from env:                 │
│    CLAUDECODE                       │
│    CLAUDE_CODE_ENTRYPOINT           │
│    TCLAW_SECRET_KEY                 │
│                                     │
│  Overridden:                        │
│    HOME → per-user home dir         │
│    ANTHROPIC_API_KEY → per-user key │
└─────────────────────────────────────┘
```

## Environment Configuration

### Local Development

```yaml
# tclaw.yaml
base_dir: /tmp/tclaw
env: local                          # default, enables OAuth browser login
server:
  addr: 127.0.0.1:9876             # default, localhost only
```

- Secrets from OS keychain (`tclaw secret set NAME value`)
- Telegram uses long polling (no `public_url`)
- Agent memory in `/tmp/tclaw/<user>/memory/`

### Docker

```yaml
# Dockerfile bakes tclaw.deploy.yaml as /etc/tclaw/tclaw.yaml
# docker-compose.yml loads .env for secrets
# Volume tclaw-data:/data for persistence
```

- Secrets from `.env` file (optional)
- Same binary, different config path

### Fly.io (Production)

```yaml
# tclaw.deploy.yaml
base_dir: /data/tclaw               # persistent Fly volume
env: prod
server:
  addr: 0.0.0.0:9876               # all interfaces (Fly proxy)
  public_url: https://tclaw.fly.dev  # enables Telegram webhooks
```

- Secrets from `fly secrets set` (pushed via `tclaw deploy secrets`)
- OAuth credentials pre-provisioned from env var on startup
- Health check at `/healthz` every 30s

## MCP Architecture

Each user gets their own MCP server on a random port (`127.0.0.1:0`). The server implements JSON-RPC over HTTP and registers tools from all `tool/` packages.

The `mcp-config.json` file is generated at `<user>/runtime/mcp-config.json` and passed to the CLI via `--mcp-config`. It includes:

1. The local tclaw MCP server (all built-in tools)
2. Any remote MCP servers the user has connected

```json
{
  "mcpServers": {
    "tclaw": {
      "type": "http",
      "url": "http://127.0.0.1:<port>/mcp"
    },
    "remote-name": {
      "type": "http",
      "url": "https://remote-server.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

## Scheduling Architecture

The scheduler runs as a background goroutine at **user lifetime** (not agent lifetime). This means:

1. Schedules fire even when the agent is idle/shut down
2. Fired messages wake the agent (lazy start)
3. The scheduler outlives individual agent sessions

```
scheduler.Run(ctx)
    │
    ├─► Loads all schedules from store
    ├─► Computes next fire time for each
    ├─► Sleeps until next fire
    │
    ▼
On fire:
    ├─► Resolves channel by name from current channel map
    ├─► Injects TaggedMessage into scheduleMsgs channel
    ├─► Updates LastFiredAt in store
    └─► Reloads schedule list (picks up creates/edits/deletes)
```
