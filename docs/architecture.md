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
| `cli/` | CLI subcommand dispatch. `serve` (start server), `chat` (TUI client), `secret` (keychain management), `deploy` (Fly.io deployment, secrets, suspend/resume), `oneshot` (single-message test mode). |
| `router/` | Per-user agent lifecycle management. Owns goroutine lifetimes, directory setup, MCP server creation, tool registration. Builds per-channel tool overrides from `config.Channel` (static) and `DynamicChannelConfig` (dynamic) tool fields. The only stateful orchestrator. |
| `agent/` | Stateless package. `Run(ctx, opts)` reads messages from channels, handles auth flows, spawns CLI subprocess per turn, streams responses back. `ChannelToolOverrides` in `Options` enables per-channel tool permissions. `reset.go` computes `allowedResetLevels()` to build dynamic reset menus filtered by channel. |
| `channel/` | Transport abstraction. `Channel` interface with implementations: Socket, Stdio, Telegram. `FanIn()` multiplexer, `ChannelMap()` helper. `DynamicStore` for runtime channel configs, `ChannelSecretKey()` for deriving secret store keys. |
| `config/` | YAML parsing, secret resolution, config validation. |

### Auth & Connections

| Package | Responsibility |
|---------|----------------|
| `oauth/` | Stateless OAuth 2.0 helpers (`BuildAuthURL`, `ExchangeCode`, `RefreshToken`). `CallbackServer` handles HTTP endpoints for OAuth callbacks, Telegram webhooks, and health checks. |
| `provider/` | OAuth provider registry. Stateless lookup by provider ID. Currently: Google. |
| `connection/` | Connection CRUD and credential management. Bridges `store.Store` (connection metadata) and `secret.Store` (encrypted credentials). Also manages remote MCP server configs. Connections and remote MCPs support channel scoping via a `Channel` field. |

### Tools (MCP)

| Package | Responsibility |
|---------|----------------|
| `mcp/` | JSON-RPC tool registry (`Handler`), HTTP server (`Server`), config file generation (`GenerateConfigFile`). |
| `mcp/discovery/` | OAuth discovery for remote MCP servers (RFC 7591 dynamic registration). Safe HTTP client that blocks private IPs and requires HTTPS. |
| `tool/channeltools/` | MCP tools for dynamic channel management (create, list, edit, delete). Stores/rotates/deletes channel secrets (e.g. Telegram bot tokens) in the secret store alongside channel config CRUD. |
| `tool/connectiontools/` | MCP tools for OAuth connection management (add, remove, list, auth_wait). |
| `tool/remotemcp/` | MCP tools for remote MCP server management (add, remove, list, auth_wait). |
| `tool/scheduletools/` | MCP tools for cron schedule management (create, list, edit, delete, pause, resume). |
| `tool/google/` | Google Workspace tools registered when a Google connection exists. Delegates to `gws` binary. |
| `tool/monzo/` | Monzo banking tools registered when a Monzo connection exists. Direct HTTP calls to the Monzo API. |
| `tool/tfl/` | Transport for London tools (line status, journey planning, arrivals, disruptions). Always registered — API key stored per-user in secret store. |
| `tool/restauranttools/` | Restaurant search and booking tools via provider interface (currently Resy). Always registered — credentials stored per-user in secret store. |
| `tool/devtools/` | MCP tools for dev workflow (dev_start, dev_status, dev_end, dev_cancel, deploy, dev_logs). Git worktree management, PR creation via `gh`, Fly.io deployment, application log inspection. |
| `tool/repotools/` | MCP tools for read-only monitoring of external git repos (add, sync, log, list, remove). Shallow clones with last-seen commit tracking. |
| `tool/onboardingtools/` | MCP tools for new user onboarding (status, set_info, advance, tip_shown, skip). Tracks onboarding progress and manages the daily tips schedule. |

### Infrastructure

| Package | Responsibility |
|---------|----------------|
| `libraries/store/` | Key-value `Store` interface with filesystem-backed implementation (`NewFS`). JSON serialization to disk. |
| `libraries/secret/` | Encrypted secret storage. `Store` interface with two implementations: `EncryptedStore` (NaCl secretbox, for deployed) and `KeychainStore` (macOS Keychain, for local dev). `Resolve()` picks the right one. |
| `libraries/logbuffer/` | Thread-safe ring buffer (`io.Writer`) for capturing slog output. Supports querying by user ID, log level, and substring. Used by `dev_logs` tool — each user only sees their own logs. |
| `libraries/id/` | TypeID generation (ULID-based). Used for schedule IDs. |
| `role/` | Role definitions, validation, and resolution. Maps named presets (`superuser`, `developer`, `assistant`) to tool lists. `Resolve(role, ChannelContext)` dynamically includes provider tool patterns (e.g. `mcp__tclaw__google_*`) and remote MCP tool patterns based on channel-scoped connections and remote MCPs. |
| `claudecli/` | Typed enums and event structs for the Claude CLI's stream-json output. Models, permission modes, tools, content block types. Pure data types, no I/O. |
| `user/` | `user.ID` and `user.Config` types. Pure data, no I/O. |
| `schedule/` | Cron schedule store and scheduler daemon. The scheduler runs at user lifetime and injects messages into channels when schedules fire. |
| `onboarding/` | Onboarding state model and store. Tracks phase progression, info gathered, and feature areas covered. Persisted as JSON in the user's state store. |
| `repo/` | Tracked repo model and store. Persists repo name, URL, branch, last-seen commit SHA, and directory paths. Backed by the user's state store. |
| `dev/` | Dev session types and store. Tracks active git worktree sessions, cached repo URL, GitHub token, and deployed commit hash. |

### CLI Tools

| Package | Responsibility |
|---------|----------------|
| `cmd/chat/` | Bubbletea TUI client (separate Go module). Connects to the agent via unix socket. Invoked via `tclaw chat`. |

## Dependency Layers

Dependencies flow strictly downward — no circular imports.

```
Layer 1:  Pure types (user, claudecli, role, store.Store interface, secret.Store interface)
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
   - Builtin commands (`stop`, `compact`, `reset`, `login`, `auth`) are gated by `isBuiltinAllowed()` — if the channel's tool permissions don't include the corresponding `builtin__*` entry, the command is denied with a message
   - `stop` cancels the active turn and clears any pending reset/auth flows
   - `reset`/`new`/`clear`/`delete` starts a per-channel reset state machine. `allowedResetLevels()` computes which reset levels to show in the menu based on channel permissions. Session resets are immediate; memories/project/everything resets call `OnReset` and project/everything return `ErrResetRequested` to restart the agent.
   - `compact` rewrites the message to a compaction prompt and falls through to `handle()`
   - `login`/`auth` are handled inline or routed to the per-channel auth state machine
   - Regular messages spawn a CLI subprocess via `handle()`
5. `handle()` calls `resolveToolsForChannel()` to pick channel-level or user-level tool permissions, filters out `builtin__*` entries, then builds CLI args and starts `claude` with stream-json output
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

### Dynamic Channel Lifecycle

```
channel_create(type: "telegram", telegram_config: {token: "..."})
    │
    ▼
Validate name, type, env (socket blocked in non-local)
    │
    ▼
Store DynamicChannelConfig in user's state (name, type, description — no token)
    │
    ▼
Store bot token in secret store (key: "channel/<name>/token")
    │
    ▼
OnChannelChange callback signals router → agent restarts automatically
    buildDynamicChannels() reads config + token from secret store
    └─► Constructs live Telegram channel with webhook/polling

channel_edit(telegram_config: {token: "new-token"})
    │
    ▼
Overwrite token in secret store (same key) — token rotation
    │
    ▼
OnChannelChange callback signals router → agent restarts automatically

channel_delete(name: "mybot")
    │
    ▼
Remove DynamicChannelConfig from store
    │
    ▼
Delete token from secret store (key: "channel/<name>/token")
    │
    ▼
OnChannelChange callback signals router → agent restarts automatically
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
    state/                     tclaw persistent data (JSON files) — NOT mounted in sandbox
    mcp-config/                MCP config JSON files (mounted read-only in sandbox)
    sessions/                  Claude CLI session IDs per channel
    secrets/                   NaCl-encrypted credentials
    repos/                     read-only repo clones (one clone per tracked repo)
    main.sock                  unix socket for "main" channel (local only)
    *.sock                     unix sockets for other channels (local only)
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

┌──────────────────────────────────────────┐
│  Runtime: secret.Store interface         │
│                                          │
│  Local: KeychainStore (macOS)            │
│  Deployed: EncryptedStore (NaCl)         │
│    - Master key: TCLAW_SECRET_KEY        │
│    - Per-user key: HKDF(master,uid)      │
│    - Files: <user>/secrets/*.enc         │
│                                          │
│  Keys:                                   │
│    anthropic_api_key      (auth)         │
│    claude_setup_token     (auth)         │
│    github_token           (dev tools)    │
│    fly_api_token          (deploy tool)  │
│    tfl_api_key            (tfl tools)    │
│    resy_api_key           (restaurant)   │
│    resy_auth_token        (restaurant)   │
│    conn/<provider>/<id>   (connections)  │
│    channel/<name>/token   (channels)     │
└──────────────────────────────────────────┘

┌─────────────────────────────────────┐
│  Subprocess isolation (allowlist)   │
│                                     │
│  Only these env var prefixes pass:  │
│    PATH, TERM, COLORTERM, LANG,    │
│    LC_*, TMPDIR, USER, LOGNAME,    │
│    SHELL, EDITOR, VISUAL, XDG_*,  │
│    TZ                               │
│                                     │
│  Overridden:                        │
│    HOME → per-user home dir         │
│    ANTHROPIC_API_KEY → per-user key │
│    CLAUDE_CODE_OAUTH_TOKEN → token  │
└─────────────────────────────────────┘
```

### Seeding Secrets from Fly

MCP tools read credentials from the per-user encrypted secret store. In production, the secret store lives on the Fly volume — tools can't read Fly secrets (env vars) directly because the subprocess env is stripped. The **seeding pattern** bridges this gap: the router reads a per-user env var at boot and writes it into the encrypted store.

**How it works:**

1. Deploy a Fly secret with a per-user name: `fly secrets set GITHUB_TOKEN_THEO=<value> -a tclaw`
2. On boot, the router checks for `GITHUB_TOKEN_<USER_ID>` in the environment
3. If found, seeds it into the user's encrypted store under the tool's key (e.g. `github_token`)
4. The env var is scrubbed from the subprocess environment (never reaches the claude CLI)
5. MCP tools read from the encrypted store as normal

**Env var naming:** `<PREFIX>_<USER_ID>` where the user ID is uppercased with non-alphanumeric chars replaced by underscores. Examples:
- User `theo` → `GITHUB_TOKEN_THEO`, `FLY_TOKEN_THEO`
- User `my-user` → `GITHUB_TOKEN_MY_USER`, `FLY_TOKEN_MY_USER`

**Currently seeded secrets:**

| Fly secret | Store key | Used by |
|------------|-----------|---------|
| `GITHUB_TOKEN_<USER>` | `github_token` | `dev_start`, `dev_end`, `deploy` (git fetch) |
| `FLY_TOKEN_<USER>` | `fly_api_token` | `deploy` (fly deploy) |
| `TFL_API_KEY_<USER>` | `tfl_api_key` | `tfl_*` tools (Transport for London) |
| `RESY_API_KEY_<USER>` | `resy_api_key` | `restaurant_*` tools (Resy search/booking) |
| `RESY_AUTH_TOKEN_<USER>` | `resy_auth_token` | `restaurant_*` tools (Resy search/booking) |
| `CLAUDE_SETUP_TOKEN_<USER>` | `claude_setup_token` | Claude CLI auth |

**When to use seeding vs runtime prompting:**

- **Seeding** — for secrets that should be pre-provisioned without user interaction (deploy tokens, CI tokens). Deploy once via `fly secrets set`, available on every boot.
- **Runtime prompting** — for secrets the user provides interactively (API keys entered in chat, OAuth flows). The tool returns an error, the agent prompts the user, stores the value.
- **Both work together** — a seeded secret can be overwritten by a runtime-provided one (e.g. user passes `fly_api_token` to the deploy tool). The most recent write wins.

**Adding a new seeded secret:**

1. Add a const key in the tool package (e.g. `flyTokenKey = "fly_api_token"`)
2. Add `<Prefix>EnvVarName(userID)` in `agent/auth.go` using `sanitizeEnvSuffix`
3. Add seeding block in `router/router.go` `startUser()` (same pattern as GitHub token)
4. Deploy the secret: `fly secrets set <ENV_VAR>=<value> -a tclaw`

## Environment Configuration

### Local Development

```yaml
# tclaw.yaml
base_dir: /tmp/tclaw
env: local                          # default, enables OAuth browser login
server:
  addr: 127.0.0.1:9876             # default, localhost only

users:
  - id: myuser
    role: superuser                 # or use allowed_tools for fine-grained control
```

- Secrets from OS keychain (`tclaw secret set NAME value`)
- Telegram uses long polling (no `public_url`)
- Agent memory in `/tmp/tclaw/<user>/memory/`

### Docker

```yaml
# Dockerfile bakes tclaw.yaml and selects prod env via --env prod
# docker-compose.yml loads .env for secrets
# Volume tclaw-data:/data for persistence
# cap_add: SYS_ADMIN for bubblewrap namespace creation
```

- Secrets from `.env` file (optional)
- Same binary and config file, `--env` flag selects the environment
- `SYS_ADMIN` capability required for bubblewrap sandbox (Fly.io allows this natively)

### Fly.io (Production)

```yaml
# tclaw.yaml (prod section)
prod:
  base_dir: /data/tclaw               # persistent Fly volume
  server:
    addr: 0.0.0.0:9876               # all interfaces (Fly proxy)
    public_url: https://your-app.fly.dev  # enables Telegram webhooks
```

- Secrets from `fly secrets set` (pushed via `tclaw deploy secrets`)
- Setup token from `fly secrets set CLAUDE_SETUP_TOKEN_<USER>=<token>` (per-user OAuth)
- Health check at `/healthz` every 30s
- `allowed_tools` must include `"mcp__tclaw__*"` (or use a role like `superuser` that includes it) — same as local config

## MCP Architecture

Each user gets their own MCP server on a random port (`127.0.0.1:0`). The server implements JSON-RPC over HTTP and registers tools from all `tool/` packages.

**Important:** The user's `allowed_tools` must include `"mcp__tclaw__*"` for the agent to use any tclaw MCP tools (connections, channels, schedules, etc.). Without this, the CLI's permission system will block MCP tool calls. Alternatively, using the `superuser` role automatically includes this pattern.

The `mcp-config.json` file is generated at `<user>/mcp-config/mcp-config.json` and passed to the CLI via `--mcp-config`. The `mcp-config/` directory is mounted read-only in the sandbox — `state/` is not mounted at all. It includes:

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

## Security Model

tclaw's security has four boundaries:

### 1. Subprocess Boundary (Environment + Filesystem Isolation)

**Environment allowlist:** The claude CLI runs with an allowlisted environment. Only safe, functional env vars are inherited (PATH, TERM, LANG, LC_*, TMPDIR, USER, SHELL, EDITOR, XDG_*, TZ). Everything else — cloud credentials (AWS_SECRET_ACCESS_KEY, GOOGLE_APPLICATION_CREDENTIALS), SSH agents (SSH_AUTH_SOCK), GitHub tokens (GITHUB_TOKEN, GH_TOKEN), and tclaw internals (TCLAW_SECRET_KEY) — is excluded by default. Explicit overrides (HOME, ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN) are always set.

**Filesystem sandbox (Linux/deployed only):** On Linux, the subprocess runs inside a bubblewrap (bwrap) mount namespace. Only explicitly bound paths are visible:
- **Read-write:** the user's memory dir and home dir
- **Read-only:** system paths (/usr, /bin, /lib, /etc/ssl, /etc/resolv.conf, etc.)
- **Private:** /tmp, /proc, /dev

The subprocess literally cannot see other users' directories, the host filesystem, or tclaw's own state files. PID and UTS namespaces are also isolated. Network is shared (the MCP server runs on localhost).

On macOS (local dev), sandboxing is skipped — the developer's own machine doesn't need protection from their own agent.

### 2. Channel Boundary (Transport Security)

**Socket and stdio channels are blocked in non-local environments.** These transports have no authentication — any process that can reach the socket file can send messages. In production, only authenticated transports (Telegram) are allowed:

- `BuildChannels()` rejects `socket` and `stdio` channel types when `env != "local"`
- `buildDynamicChannels()` skips socket channels in non-local environments; Telegram dynamic channels work everywhere
- The `channel_create` MCP tool returns an error when creating a socket channel in a non-local environment
- Telegram bot tokens are stored in the encrypted secret store (not in the channel config JSON) and cleaned up on channel deletion

Telegram channels support an `allowed_users` list of Telegram user IDs. When set, messages from users not in the list are silently dropped at the handler level before reaching the agent. This prevents strangers who discover a bot's username from interacting with it. The allowlist is configured in `telegram.allowed_users` (static config) or via the `telegram_config.allowed_users` field in `channel_create`/`channel_edit` (dynamic channels).

This means production deployments can only communicate via Telegram (which authenticates via bot token + webhook secret). Socket channels are available in local dev where the threat model is the developer's own machine.

### 3. MCP Tool Boundary

The agent interacts with tclaw state (connections, schedules, channels, secrets) only through MCP tools served on a per-user localhost port. Tool calls are:
- **Audit logged** with tool name, duration, and success/failure status
- **Size limited** (1 MiB max request body)
- **Permission gated** via Claude Code's `allowed_tools` config (must include `"mcp__tclaw__*"`)

### Per-Channel Tool Permissions

Tool permissions are resolved per-channel at each turn. Channels can define their own `allowed_tools` and `disallowed_tools` lists, or use a **role** as a named preset (in static config or dynamic channel configs). Roles and explicit tool lists are mutually exclusive — setting a role clears explicit lists and vice versa. Roles are resolved via `role.Resolve()`, which dynamically includes provider tool patterns (e.g. `mcp__tclaw__google_*`) for channel-scoped connections and remote MCP tool patterns for channel-scoped remote MCPs. When a channel has overrides, they **replace** the user-level permissions entirely — no merging.

This gates two layers:
- **CLI tools** — `resolveToolsForChannel()` picks the channel-level or user-level allowed/disallowed lists (resolving roles to tool lists first), filters out `builtin__*` entries (which the CLI doesn't understand), and passes the result as `--allowedTools`/`--disallowedTools` flags to the subprocess.
- **Builtin commands** — `isBuiltinAllowed()` checks whether `builtin__*` entries (e.g. `builtin__stop`, `builtin__compact`, `builtin__reset`, `builtin__login`, `builtin__auth`) are present in the channel's resolved tool list. If no `builtin__*` entries exist at all (neither channel nor user level), everything is allowed for backwards compatibility. The reset menu is dynamic — `allowedResetLevels()` only includes levels whose corresponding builtin (e.g. `builtin__reset_session`, `builtin__reset_memories`) is permitted.

The router builds the `ChannelToolOverrides` map from two sources:
- **Static channels** — tool fields (including role) from `config.Channel` entries, matched to live channels by name
- **Dynamic channels** — tool fields (including role) from `DynamicChannelConfig` in the store, matched to live channels by name

### 4. Secret Boundary

Credentials are encrypted at rest using NaCl secretbox with per-user derived keys:
- Master key from `TCLAW_SECRET_KEY` env var (stripped from subprocess env)
- Per-user key derived via HKDF (SHA-256) with user ID as info
- Files stored with 0o600 permissions

Secret store keys follow a hierarchical naming convention:
- `anthropic_api_key` — user's Anthropic API key
- `claude_setup_token` — OAuth setup token
- `github_token` — GitHub PAT for dev workflow (push, PR creation)
- `fly_api_token` — Fly.io API token for deploys
- `tfl_api_key` — TfL API key for Transport for London tools
- `resy_api_key` — Resy API key for restaurant tools
- `resy_auth_token` — Resy auth token for restaurant tools
- `conn/<provider>/<id>` — OAuth connection credentials
- `channel/<name>/token` — dynamic channel secrets (e.g. Telegram bot tokens)

OAuth tokens are auto-refreshed and never exposed in logs or subprocess environments. Channel secrets are created alongside dynamic channels and cleaned up on deletion. Deploy tokens are passed to `fly secrets set` via stdin (not CLI args) to avoid exposure in process listings.

### Input Validation

- **Session IDs** loaded from disk are validated (non-empty, max 256 chars, no control characters)
- **Setup tokens** are validated after extraction (min 50 chars, alphanumeric/hyphens/underscores only)
- **API keys** require the `sk-ant-` prefix and minimum length of 50 characters
- **OAuth callbacks** use state codes with TTL and per-state rate limiting to prevent brute-force
- **Remote MCP URLs** are validated against SSRF (HTTPS required, private IP ranges blocked)
- **Dynamic channels** — names validated (alphanumeric/hyphens/underscores, max 64 chars), type must match allowed set for the environment (socket blocked in non-local), `telegram_config` required for telegram type, uniqueness enforced against both static and dynamic channels

### What the Subprocess CAN Access

- Its own memory directory (read/write via CWD)
- Claude Code internal state (via HOME)
- MCP config files (read-only — `mcp-config/` directory)
- The MCP server on localhost (tool calls)
- Standard system utilities (via PATH)
- Read-only system paths (libraries, certs, DNS)

### What the Subprocess CANNOT Access

- Cloud provider credentials (AWS, GCP, Azure) — excluded by env allowlist
- SSH agent sockets — excluded by env allowlist
- GitHub/GitLab tokens — excluded by env allowlist
- tclaw's master encryption key — excluded by env allowlist
- Other users' data — invisible in bwrap mount namespace (deployed)
- Host filesystem outside bound paths — invisible in bwrap (deployed)
- tclaw state files (connections, schedules, channels, remote MCPs) — not mounted in sandbox, only accessible via MCP tools
- Encrypted secrets (credentials, tokens) — not mounted in sandbox

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
