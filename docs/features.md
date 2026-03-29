# tclaw Features

tclaw is a multi-user Claude Code host that spawns isolated `claude` CLI subprocesses, managing communication through multiple channels (unix sockets, stdio, Telegram), with persistent memory, OAuth connections, scheduling, and MCP tool extensibility.

> **Note:** Agent runtime behavior (tool usage, formatting, memory) is defined in `agent/system_prompt.md`. Implementation details (package structure, data flows, security) are in `docs/architecture.md`. MCP tool descriptions (`definitions.go` and inline tool defs) are the primary reference for tool parameters and usage — the agent reads these directly at runtime. This file covers developer-facing feature summaries that don't fit in either place.

## Multi-Turn Conversations

- **Session continuity** — each channel gets its own Claude session. The agent resumes via `--resume <session-id>` so context carries across messages.
- **Streaming responses** — text deltas, thinking blocks, tool calls, and tool results are streamed to the channel in real time as the CLI emits them.
- **Turn stats** — after each turn the agent reports turn count, wall-clock time, cost, and which model(s) were used.
- **Max turns** — configurable per user (defaults to 10) to limit agentic loops.
- **Stop/interrupt** — typing `stop` cancels the active turn immediately via context cancellation.
- **Reset menu** — typing `new`, `reset`, `clear`, or `delete` opens a multi-option reset menu (session, memories, project, everything). The menu adapts dynamically per channel based on tool permissions.
- **Compact** — typing `compact` triggers context compaction. Works on all channels.

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

Channels are the transport layer between users and the agent. All channels are defined in `tclaw.yaml` under each user's `channels` list. There is no distinction between "static" and "dynamic" channels — all channels are equal. The agent can create, edit, and delete channels at runtime via MCP tools (`channel_create`, `channel_edit`, `channel_list`, `channel_delete`), which mutate the config file directly.

### Channel Types

- **Socket** — unix domain sockets. The TUI chat client (`cmd/chat`) connects here. **Local only** — blocked in non-local environments because sockets have no authentication.
- **Stdio** — standard input/output for simple pipe-based usage. **Local only**.
- **Telegram** — Telegram Bot API. Uses long polling locally, webhooks in production. Supports HTML markup for rich text. The only channel type allowed in production. Access is restricted to the user's Telegram ID (configured at the user level).

### Config as Desired State

`tclaw.yaml` is the single source of truth. Channel tools (`channel_create`, `channel_edit`, `channel_delete`, `channel_done`) all write directly to the config file via `config.Writer`, which uses atomic temp-file-plus-rename writes to prevent partial state.

On startup (and after config mutations), the **reconciler** compares desired state (config) against actual state (runtime) and converges them:

- **No provisioner for this type** (socket, stdio): immediately ready.
- **Provisioner says already ready**: ready.
- **Not ready, can auto-provision**: provisions platform resources (e.g. creates a Telegram bot), stores runtime state, then ready.
- **Not ready, can't auto-provision**: marked as `needs_setup` so the agent can guide the user through manual setup.

This means a Telegram channel can be added to config without a bot token — the reconciler will provision the bot automatically if the Telegram Client API credentials are available.

### Config Sync

`tclaw config sync` and `tclaw config diff` manage config between local and remote environments. `diff` shows what would change; `sync` pushes the local config to the remote deployment.

### Runtime State

Platform-specific metadata that doesn't belong in the config file (Telegram chat IDs, bot usernames for teardown) is stored separately in `RuntimeStateStore`. Each channel gets its own runtime state keyed by name. The reconciler populates runtime state during provisioning; it persists across agent restarts.

### Telegram User ID

Telegram identity is configured at the **user level**, not per-channel:

```yaml
users:
  - id: myuser
    telegram:
      user_id: "123456789"
    channels:
      - type: telegram
        name: admin
        description: Primary admin channel
```

All Telegram channels for a user inherit the user-level `telegram.user_id`. Validation enforces that any user with Telegram channels must have this set.

### Ephemeral Channels

Set `ephemeral: true` on `channel_create` for channels that should auto-delete after an idle timeout (default 24h). Use `channel_done` to tear down manually — it sends a confirmation prompt to the user and only proceeds when the user replies "yes" (async flow via the router). Platform resources (bots, tokens) are cleaned up automatically, and the channel is removed from config.

### Platform Abstraction

Channel tools are platform-agnostic — the agent creates channels without needing to know the underlying transport. Platform-specific logic (bot creation, teardown, notifications) is handled by `EphemeralProvisioner` implementations behind the scenes. Each channel's runtime state holds `PlatformState` (e.g. chat ID for Telegram) and `TeardownState` (e.g. bot username) as typed discriminator structs with a `Type` field and platform-specific pointer fields.

### Tool Groups

Tool groups control which tools are available on each channel. See `tool_group_list` for all available groups with descriptions. Groups are additive — channels start with nothing and add what they need. `creatable_groups` controls what groups a channel can assign to channels it creates (privilege escalation prevention).

## Memory System

Per-user data is split into four zones with clear access boundaries (see `docs/architecture.md` for the full directory layout):

1. **Agent memory** (`<user>/memory/`) — the agent reads and writes freely. Contains `CLAUDE.md` and topic subfiles.
2. **Claude Code state** (`<user>/home/.claude/`) — internal CLI state. Off limits to the agent.
3. **tclaw state** (`<user>/state/`, `sessions/`, `secrets/`) — not mounted in the sandbox. Accessible only via MCP tools.
4. **MCP config** (`<user>/mcp-config/`) — mounted read-only in the sandbox for `--mcp-config`.

Each user's `CLAUDE.md` is seeded on first startup with a template (`agent.DefaultMemoryTemplate`).

## System Prompt

The system prompt is the **single source of truth** for how the agent behaves at runtime. It's built from a Go template (`agent/system_prompt.md`) and includes agent identity, channel list, active channel context, and behavioral rules. When adding or changing agent-facing behavior, update `agent/system_prompt.md`.

## Authentication

### API Key

Users can be configured with an Anthropic API key in the config file (supports `${secret:NAME}` references). Keys can also be entered interactively via the chat channel.

### OAuth (Claude Pro/Teams)

On local environments, the agent can run `claude setup-token` which opens the browser for OAuth and generates a long-lived (1 year) setup token. The token can then be deployed to production via `fly secrets set`.

### Interactive Auth Flow

When the CLI reports `authentication_failed`, the agent automatically starts an interactive auth flow: presents a choice (OAuth, API key, or cancel), handles the selected path, stores credentials in the encrypted secret store, and retries the original message.

## Connections & Remote MCPs

OAuth connections and remote MCP servers are managed via `connection_*` and `remote_mcp_*` tools. Every connection is scoped to a specific channel — provider tools are only available on the owning channel. See tool descriptions for the full API.

### Providers

- **Google Workspace** — Gmail, Drive, Calendar, Docs, Sheets, Slides, Tasks via `google_*` tools.
- **Monzo** — Banking tools via `monzo_*` tools. Requires Monzo API client ID/secret in config (`providers.monzo`). Create at [developers.monzo.com](https://developers.monzo.com/).
- **TfL** — Transport for London via `tfl_*` tools. Always registered, optional API key from [api-portal.tfl.gov.uk](https://api-portal.tfl.gov.uk/products).
- **Resy** — Restaurant booking via `restaurant_*` tools. Credentials from browser dev tools.
- **Enable Banking** — Open Banking (PSD2) via `banking_*` tools. Free at [enablebanking.com](https://enablebanking.com/).
- **Telegram Client API** — MTProto via `telegram_client_*` tools. Bot management, chat ops. Credentials from [my.telegram.org](https://my.telegram.org).

Each provider's tool descriptions contain full setup and usage guidance.

## Secret Management

See `docs/architecture.md` for the full encryption model, secret store keys, and seeding pattern.

- **Config-level:** `${secret:NAME}` syntax resolves from OS keychain then env vars.
- **Runtime:** Per-user encrypted storage (NaCl secretbox, HKDF-derived keys).
- **Collection:** `secret_form_request` / `secret_form_wait` tools create secure web forms. The `credentialerror` package provides a standard `CREDENTIALS_NEEDED` error format that tools return when credentials are missing — the agent detects this and automatically invokes the secret form flow.
- **Keychain CLI:** `tclaw secret set/get/delete` for local keychain management. `tclaw deploy secrets` pushes to Fly.

## Oneshot Mode

`tclaw oneshot` sends a single message, prints the response, and exits. Useful for quick local testing.

```
tclaw oneshot [flags] <message>
  --config   Config file (default: tclaw.yaml)
  --env      Environment (default: local)
  --user     User ID (default: first user)
  --telegram Emulate Telegram formatting
  --debug    Log raw CLI event JSON
```

## TUI Chat Client

The `cmd/chat` binary is a Bubbletea-based terminal UI that connects via unix socket. Multi-line input, scrollable history, streaming output, auto-reconnect.

## Onboarding

New users are guided through a structured onboarding flow: welcome -> info gathering -> daily tips -> complete. Managed via `onboarding_*` tools. State persisted in the user's state store.

## HTTP Server

- **`/healthz`** — health check (returns 200)
- **`/oauth/callback`** — OAuth provider redirects
- **`/secret-form/{state}`** — secure credential collection forms
- **`/telegram/<channel_name>`** — Telegram webhook endpoints (production only)
