# tclaw Features

tclaw is a multi-user Claude Code host that spawns isolated `claude` CLI subprocesses, managing communication through multiple channels (unix sockets, stdio, Telegram), with persistent memory, OAuth connections, scheduling, and MCP tool extensibility.

## Multi-Turn Conversations

- **Session continuity** — each channel gets its own Claude session. The agent resumes via `--resume <session-id>` so context carries across messages.
- **Streaming responses** — text deltas, thinking blocks, tool calls, and tool results are streamed to the channel in real time as the CLI emits them.
- **Turn stats** — after each turn the agent reports turn count, wall-clock time, and cost.
- **Max turns** — configurable per user (defaults to 10) to limit agentic loops.
- **Stop/interrupt** — typing `stop` cancels the active turn immediately via context cancellation.
- **Reset menu** — typing `new`, `reset`, `clear`, or `delete` opens a multi-option reset menu. The menu is dynamic — only reset levels allowed on the current channel are shown (see [Per-Channel Tool Permissions](#per-channel-tool-permissions)):
  1. **Session** — clear the current channel's conversation session
  2. **Memories** — erase all memory files (CLAUDE.md and topic files), requires confirmation
  3. **Project** — clear Claude Code state and all sessions across channels, keeps memories/connections/schedules/secrets, requires confirmation
  4. **Everything** — erase all user data (memories, state, sessions, connections, secrets), requires confirmation. The agent restarts and re-seeds a fresh CLAUDE.md after project/everything resets.
- **Compact** — typing `compact` triggers context compaction (summarize and discard verbose history). Works on all channels.

## Lazy Agent Lifecycle

Agents are not started at boot. They start lazily on the first inbound message and shut down automatically after 10 minutes of inactivity. The next message restarts the agent transparently, preserving session state across restarts.

## Multi-User Isolation

Each user gets a fully isolated environment:

- **Separate HOME directory** — Claude Code's internal state (`~/.claude/`) is scoped per user.
- **Separate memory** — each user has their own `CLAUDE.md` and topic files.
- **Separate sessions** — per-channel session IDs persisted independently.
- **Separate secrets** — encrypted credential storage per user (NaCl secretbox).
- **Separate API keys** — each user can have their own Anthropic API key.
- **Separate MCP tools** — each user gets their own MCP server on a random port.
- **Environment allowlist** — the subprocess only inherits safe env vars (PATH, TERM, LANG, etc.). Cloud credentials, SSH agents, GitHub tokens, and tclaw internals are excluded by default.
- **Filesystem sandbox** (deployed only) — on Linux, each subprocess runs in a bubblewrap mount namespace. Only the user's own memory and home dirs are visible; other users' data and the host filesystem are inaccessible.

## Channels

Channels are the transport layer between users and the agent.

### Static Channels (from config)

- **Socket** — unix domain sockets. The TUI chat client (`cmd/chat`) connects here. Supports streaming edits (send-then-edit pattern). **Local only** — blocked in non-local environments because sockets have no authentication.
- **Stdio** — standard input/output for simple pipe-based usage. **Local only** — blocked in non-local environments.
- **Telegram** — Telegram Bot API. Uses long polling locally, webhooks in production. Supports HTML markup for rich text. Status messages (thinking, tool use) are separated from response text into distinct messages. The only channel type allowed in production.

### Dynamic Channels (runtime-created)

The agent can create, edit, and delete channels at runtime via MCP tools. Dynamic channels persist across agent restarts (stored in the user's state directory). The agent sees both static and dynamic channels in its system prompt.

Supported dynamic channel types:
- **Socket** — local environments only (no authentication). Created with `type: "socket"`.
- **Telegram** — all environments. Created with `type: "telegram"` and a nested `telegram_config` object containing the bot token.

Channel type is validated at creation time — attempting to create a socket channel in a non-local environment returns an error.

**MCP tools:**

- **channel_create** — creates a dynamic channel. Requires `name`, `description`, and `type` ("socket" or "telegram"). Telegram channels require a `telegram_config` with a `token` field. The bot token is stored in the encrypted secret store (not in the channel config JSON). Optionally accepts `allowed_tools` and `disallowed_tools` to set per-channel tool permissions. Returns an error if the channel type is not allowed in the current environment.
- **channel_edit** — updates a dynamic channel's description, rotates its Telegram bot token (via `telegram_config`), and/or updates `allowed_tools` and `disallowed_tools`. At least one field must be provided. Cannot edit static channels.
- **channel_delete** — removes a dynamic channel and cleans up any associated secrets (e.g. Telegram bot token from the secret store). Cannot delete static channels.
- **channel_list** — lists all channels (static and dynamic) with name, type, description, source, and tool permissions (`allowed_tools`/`disallowed_tools`).

**Secret lifecycle:** Telegram bot tokens follow a strict lifecycle tied to their channel — created in the secret store on `channel_create`, rotated via `channel_edit`, and deleted on `channel_delete`. Tokens are never stored in the channel config JSON and are only read from the secret store when building the live Telegram channel on agent restart.

### Environment Filtering

Each channel can specify which environments it's active in via the `envs` field. A channel with `envs: [prod]` only starts in production. Empty `envs` means active everywhere.

### Per-Channel Tool Permissions

Channels can override the user-level `allowed_tools` and `disallowed_tools` to restrict or customize what the agent can do on each channel. This works for both static channels (in config) and dynamic channels (via MCP tools).

**How it works:**

- Each channel can define `allowed_tools` and/or `disallowed_tools` lists.
- When a channel sets tool permissions, they **replace** the user-level defaults entirely — there is no merging. This gives full control over what's available per channel.
- When a channel has no tool overrides, the user-level `allowed_tools` and `disallowed_tools` apply as before.

**Builtin command gating:**

Builtin commands (stop, compact, reset, login/auth) can be gated using `builtin__*` tool names in the allow/disallow lists:

| Tool name | Controls |
|-----------|----------|
| `builtin__stop` | The `stop` command |
| `builtin__compact` | The `compact` command |
| `builtin__login` / `builtin__auth` | The `login` and `auth` commands |
| `builtin__reset` | All reset levels (wildcard) |
| `builtin__reset_session` | Session reset only |
| `builtin__reset_memories` | Memories reset only |
| `builtin__reset_project` | Project reset only |
| `builtin__reset_all` | Everything reset only |

When a user tries a command that's not allowed on the current channel, the agent responds with "This command is not available on this channel."

The reset menu adapts dynamically — it only shows reset levels that are allowed on the current channel. If only `builtin__reset_session` is allowed, the menu shows just the session option.

**Backwards compatibility:**

- No channel overrides and no builtin entries anywhere: all builtins are allowed, user-level tool permissions apply.
- Builtin tool names only matter when they appear in an allow/disallow list. Omitting them entirely means all builtins are permitted.

**Config example (static channel):**

```yaml
channels:
  - name: restricted
    type: telegram
    description: "Read-only channel — no resets, no auth"
    allowed_tools:
      - "mcp__tclaw__*"
      - Bash
      - Read
    disallowed_tools:
      - "builtin__reset"
      - "builtin__login"
```

**Dynamic channel example:**

The `channel_create` and `channel_edit` MCP tools accept `allowed_tools` and `disallowed_tools` parameters. The `channel_list` tool includes these fields in its output.

### Channel Setup Patterns

There are two approaches to setting up channels, each suited to different deployment scenarios.

#### Approach 1: Admin + Dynamic Assistant (recommended for power users)

The admin channel is defined statically in config with full tool access including `channel_create`. The admin channel then creates additional channels (like an "assistant" channel) at runtime via the `channel_create` MCP tool with a restricted tool set.

**Why this approach:** The admin creates and manages channels conversationally — no deploy needed to add/modify/remove channels. Tool permissions can be iterated on by editing the dynamic channel. Good for users who want flexibility and can manage their own channel setup.

**Setup flow:**
1. Static admin channel in config with full tools + `mcp__tclaw__channel_*` + all builtins
2. User messages admin channel: "set up an assistant channel on Telegram"
3. Agent guides user through @BotFather token creation
4. Agent calls `channel_create` with restricted `allowed_tools` (no dev tools, no channel management, restricted reset)
5. Agent restarts, new channel is live

**Example admin config:**
```yaml
channels:
  - name: admin
    type: telegram
    description: Primary admin channel
    telegram:
      token: ${secret:TELEGRAM_ADMIN_TOKEN}
    allowed_tools:
      - Bash
      - Read
      - Edit
      - Write
      - Glob
      - Grep
      - WebFetch
      - WebSearch
      - Agent
      - "mcp__tclaw__channel_*"
      - "mcp__tclaw__schedule_*"
      - "mcp__tclaw__connection_*"
      - "mcp__tclaw__remote_mcp_*"
      - "builtin__reset"
      - "builtin__stop"
      - "builtin__compact"
      - "builtin__login"
```

The assistant channel is then created dynamically with tools like:
```json
{
  "allowed_tools": [
    "Read", "WebFetch", "WebSearch",
    "mcp__tclaw__google_*", "mcp__tclaw__schedule_*",
    "mcp__tclaw__connection_*",
    "builtin__reset_session", "builtin__reset_memories",
    "builtin__stop", "builtin__compact"
  ]
}
```

#### Approach 2: All Static Channels (recommended for managed deployments)

All channels are defined in the config file. The assistant channel is pre-configured with its tool set. No channel management tools are needed.

**Why this approach:** Simpler, more predictable. Good for deployments where the channel setup is known in advance and managed by whoever controls the config file. No risk of accidentally deleting a channel via a tool call.

**Example config:**
```yaml
channels:
  - name: admin
    type: telegram
    description: Primary admin channel
    telegram:
      token: ${secret:TELEGRAM_ADMIN_TOKEN}
    allowed_tools:
      - Bash
      - Read
      - Edit
      - Write
      - WebFetch
      - WebSearch
      - "mcp__tclaw__schedule_*"
      - "mcp__tclaw__connection_*"
      - "builtin__reset"
      - "builtin__stop"
      - "builtin__compact"
      - "builtin__login"
  - name: assistant
    type: telegram
    description: Mobile assistant — concise responses, no dev tools
    telegram:
      token: ${secret:TELEGRAM_ASSISTANT_TOKEN}
    allowed_tools:
      - Read
      - WebFetch
      - WebSearch
      - "mcp__tclaw__google_*"
      - "mcp__tclaw__schedule_*"
      - "builtin__reset_session"
      - "builtin__reset_memories"
      - "builtin__stop"
      - "builtin__compact"
```

#### Choosing an approach

| Consideration | Dynamic (Approach 1) | Static (Approach 2) |
|---------------|---------------------|---------------------|
| Adding/removing channels | Conversational, no deploy | Requires config change + deploy |
| Iterating tool permissions | `channel_edit` at runtime | Config change + deploy |
| Risk of accidental deletion | Possible via `channel_delete` | Not possible (static) |
| Multi-user managed deployments | Less predictable | More controlled |
| Best for | Power users, single-user setups | Managed/team deployments |

## Memory System

### Three-Zone Directory Model

Per-user data is split into three zones with clear access boundaries:

1. **Agent memory** (`<user>/memory/`) — the agent reads and writes freely. This is the subprocess CWD and is also passed via `--add-dir`. Contains `CLAUDE.md` (the real file) and topic subfiles.
2. **Claude Code state** (`<user>/home/.claude/`) — internal CLI state (conversation history, settings, plans). Off limits to the agent. A symlink at `home/.claude/CLAUDE.md` → `../../memory/CLAUDE.md` bridges the CLI's auto-load with the agent's sandbox.
3. **tclaw state** (`<user>/state/`, `sessions/`, `secrets/`, `runtime/`) — accessible only via MCP tools, outside the agent's HOME entirely.

### CLAUDE.md

Each user's `CLAUDE.md` is seeded on first startup with a template that explains how to use it. The agent can update it to remember things across sessions. Topic-specific files can be created in the memory directory and referenced from `CLAUDE.md` using `@filename.md` syntax.

## System Prompt

The system prompt is built from a Go template (`agent/system_prompt.md`) and includes:

- Today's date
- A list of all channels with their names, types, descriptions, and sources
- The currently active channel (appended per-turn)
- User-defined custom prompt (from config `system_prompt` field)

## Authentication

### API Key

Users can be configured with an Anthropic API key in the config file (supports `${secret:NAME}` references). Keys can also be entered interactively via the chat channel — the agent detects auth failures and prompts the user to authenticate.

### OAuth (Claude Pro/Teams)

On local environments, the agent can run `claude setup-token` which opens the browser for OAuth and generates a long-lived (1 year) setup token. After generation:

1. The token is captured from stdout
2. The user is asked whether to deploy the token to production via `fly secrets set`
3. In production, the setup token is passed to the CLI subprocess as `CLAUDE_CODE_OAUTH_TOKEN` — no file provisioning needed

### Interactive Auth Flow

When the CLI reports `authentication_failed`, the agent automatically starts an interactive auth flow:

1. Presents a choice: OAuth login, API key, or cancel
2. For OAuth: launches `claude setup-token` in a background goroutine
3. For API key: prompts for and validates the key (must start with `sk-ant-`)
4. Stores credentials in the encrypted secret store
5. Retries the original message after successful auth

## Connections (OAuth Providers)

Users can connect external services (currently Google Workspace) through OAuth flows managed via MCP tools:

- **connection_add** — starts the OAuth flow for a provider. Opens a browser for consent.
- **connection_remove** — disconnects and wipes credentials.
- **connection_list** — lists all active connections.
- **connection_auth_wait** — polls for OAuth completion (used by the agent after starting a flow).

OAuth callbacks are handled by the HTTP server at `/oauth/callback`. The callback server includes rate limiting per state code to prevent brute-force attacks.

## Google Workspace Integration

When a Google connection is established, provider-specific MCP tools are registered:

- **google_gmail_list** — searches and lists Gmail messages with full metadata (subject, from, to, date, snippet, labels) in a single call. Gmail's list API only returns message IDs, so this wrapper automatically fetches metadata for each result concurrently. Defaults to 10 messages (max 25). Supports Gmail search syntax via the `query` parameter (e.g. `from:alice@example.com`, `is:unread`, `after:2026/03/01`). Use this for scanning/searching email; use `google_workspace` with `gmail users messages get` for reading a single email's full body.
- **google_workspace** — sends commands to the `gws` binary (Google Workspace CLI) with the user's OAuth token. Supports Gmail, Drive, Calendar, Docs, Sheets, Slides, and Tasks. Use `google_workspace_schema` to discover available methods and parameters.
- **google_workspace_schema** — looks up the schema for a Google Workspace API method (e.g. `gmail.users.messages.list`, `drive.files.list`). Returns parameter details, request/response schemas, and descriptions.

Tools are only registered when a Google connection exists and are removed when the last connection is disconnected. Services available depend on the OAuth scopes granted during consent.

## Remote MCP Servers

Users can connect to external MCP servers (like the Anthropic MCP directory) via MCP tools:

- **remote_mcp_add** — adds a remote MCP server URL with optional OAuth discovery (RFC 7591 dynamic client registration).
- **remote_mcp_remove** — disconnects a remote MCP.
- **remote_mcp_list** — lists connected remote MCPs.
- **remote_mcp_auth_wait** — polls for OAuth completion on remote MCPs that require auth.

Remote MCPs are included in the `mcp-config.json` file that's passed to the Claude CLI via `--mcp-config`. Bearer tokens from OAuth are automatically included.

### Security

The MCP discovery client (`mcp/discovery/safeclient.go`) validates that remote MCP URLs:

- Use HTTPS (not HTTP)
- Don't resolve to private IP ranges (RFC 1918, loopback, link-local, CGN)
- Don't resolve to IPv6 loopback or unspecified addresses

## Scheduling

The agent can create and manage cron schedules that fire autonomously:

- **schedule_create** — creates a cron schedule with a prompt, channel, and cron expression.
- **schedule_edit** — modifies an existing schedule.
- **schedule_delete** — removes a schedule.
- **schedule_pause** / **schedule_resume** — pauses or resumes a schedule.
- **schedule_list** — lists all schedules with their status and next fire time.

When a schedule fires, it injects a message into the specified channel, waking the agent if it's idle. Schedules persist across agent restarts and are managed by a background goroutine that runs at user lifetime (not agent lifetime).

## Secret Management

### Config-level secrets

The `${secret:NAME}` syntax in config files resolves secrets by:

1. Trying the OS keychain first (macOS Keychain via `security` command)
2. Falling back to environment variables
3. Scrubbing the env var after resolution so subprocesses can't read it

### Runtime secrets

Per-user encrypted storage using NaCl secretbox:

- Master key from `TCLAW_SECRET_KEY` env var
- Per-user key derived via HKDF (SHA-256)
- Files stored with 0o600 permissions in the user's `secrets/` directory
- Used for API keys, OAuth tokens, and channel secrets (e.g. Telegram bot tokens)

Secret store keys follow a hierarchical naming convention:
- `anthropic_api_key` — user's Anthropic API key
- `claude_setup_token` — OAuth setup token
- `conn/<provider>/<id>` — OAuth connection credentials (auto-refreshed)
- `channel/<name>/token` — dynamic channel secrets (lifecycle tied to channel CRUD)

### Keychain tool

The `cmd/secret` binary provides:

- `secret set <name> <value>` — store in keychain
- `secret get <name>` — retrieve from keychain
- `secret delete <name>` — delete from keychain
- `secret deploy-secrets <config> [app]` — scan config for `${secret:NAME}` refs, read each from keychain, push to Fly.io

## TUI Chat Client

The `cmd/chat` binary is a Bubbletea-based terminal UI that connects to the agent via unix socket:

- Input area with multi-line support (Shift+Enter for newlines, Enter to send)
- Scrollable message history with timestamps
- All builtin commands (`reset`, `compact`, `stop`, etc.) are sent to the agent — the chat client no longer intercepts them
- Visual separation between user and assistant messages
- Auto-reconnect on socket connection
- Shows streaming output in real time via edit-in-place

## HTTP Server

The HTTP server handles:

- **`/healthz`** — health check endpoint (returns 200)
- **`/oauth/callback`** — OAuth provider redirects
- **`/telegram/<channel_name>`** — Telegram webhook endpoints (production only)
