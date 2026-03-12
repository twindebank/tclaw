# tclaw Features

tclaw is a multi-user Claude Code host that spawns isolated `claude` CLI subprocesses, managing communication through multiple channels (unix sockets, stdio, Telegram), with persistent memory, OAuth connections, scheduling, and MCP tool extensibility.

## Multi-Turn Conversations

- **Session continuity** — each channel gets its own Claude session. The agent resumes via `--resume <session-id>` so context carries across messages.
- **Streaming responses** — text deltas, thinking blocks, tool calls, and tool results are streamed to the channel in real time as the CLI emits them.
- **Turn stats** — after each turn the agent reports turn count, wall-clock time, and cost.
- **Max turns** — configurable per user (defaults to 10) to limit agentic loops.
- **Stop/interrupt** — typing `stop` cancels the active turn immediately via context cancellation.
- **Session reset** — typing `new`, `reset`, `clear`, or `delete` clears the channel's session so the next message starts fresh.

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

- **Socket** — unix domain sockets. The TUI chat client (`cmd/chat`) connects here. Supports streaming edits (send-then-edit pattern).
- **Stdio** — standard input/output for simple pipe-based usage.
- **Telegram** — Telegram Bot API. Uses long polling locally, webhooks in production. Supports HTML markup for rich text. Status messages (thinking, tool use) are separated from response text into distinct messages.

### Dynamic Channels (runtime-created)

The agent can create, edit, and delete channels at runtime via MCP tools. Dynamic channels are socket-based and persist across agent restarts (stored in the user's state directory). The agent sees both static and dynamic channels in its system prompt.

### Environment Filtering

Each channel can specify which environments it's active in via the `envs` field. A channel with `envs: [prod]` only starts in production. Empty `envs` means active everywhere.

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

- **google_workspace** — sends commands to the `gws` binary (Google Workspace CLI) with the user's OAuth token. Supports Gmail, Drive, Calendar, Docs, Sheets, Slides, and Tasks.

Services are derived from the OAuth scopes granted during consent.

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
- Used for API keys and OAuth tokens

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
- Commands: `new`/`reset` (clear session), `stop` (interrupt), `compact` (compact context), `quit`/`exit` (disconnect)
- Visual separation between user and assistant messages
- Auto-reconnect on socket connection
- Shows streaming output in real time via edit-in-place

## HTTP Server

The HTTP server handles:

- **`/healthz`** — health check endpoint (returns 200)
- **`/oauth/callback`** — OAuth provider redirects
- **`/telegram/<channel_name>`** — Telegram webhook endpoints (production only)
