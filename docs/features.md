# tclaw Features

tclaw is a multi-user Claude Code host that spawns isolated `claude` CLI subprocesses, managing communication through multiple channels (unix sockets, stdio, Telegram), with persistent memory, OAuth connections, scheduling, and MCP tool extensibility.

> **Note:** The agent's runtime behavior (how it uses tools, formats messages, manages memory) is defined in `agent/system_prompt.md`. This file documents tclaw's features from the **developer's perspective** — how things are implemented, configured, and wired together. If you're looking for what the agent sees and how it behaves, read the system prompt.

## Multi-Turn Conversations

- **Session continuity** — each channel gets its own Claude session. The agent resumes via `--resume <session-id>` so context carries across messages.
- **Streaming responses** — text deltas, thinking blocks, tool calls, and tool results are streamed to the channel in real time as the CLI emits them.
- **Turn stats** — after each turn the agent reports turn count, wall-clock time, cost, and which model(s) were used.
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

### Channel Types

- **Socket** — unix domain sockets. The TUI chat client (`cmd/chat`) connects here. Supports streaming edits (send-then-edit pattern). **Local only** — blocked in non-local environments because sockets have no authentication.
- **Stdio** — standard input/output for simple pipe-based usage. **Local only** — blocked in non-local environments.
- **Telegram** — Telegram Bot API. Uses long polling locally, webhooks in production. Supports HTML markup for rich text. Status messages (thinking, tool use) are separated from response text into distinct messages. The only channel type allowed in production. Supports an `allowed_users` list to restrict which Telegram user IDs can interact with the bot.

### Static vs Dynamic Channels

**Static channels** are defined in the config file (`tclaw.yaml`). **Dynamic channels** are created at runtime via MCP tools (channel_create, channel_edit, channel_delete). Dynamic channels persist across agent restarts (stored in the user's state directory). Channel mutations trigger an automatic agent restart.

Dynamic channel types: Socket (local only) and Telegram (all environments). Type is validated at creation — socket channels in non-local environments return an error.

**Secret lifecycle:** Telegram bot tokens follow a strict lifecycle tied to their channel — created in the secret store on `channel_create`, rotated via `channel_edit`, and deleted on `channel_delete`. Tokens are never stored in the channel config JSON.

### Telegram User Allowlist

Telegram bots are public by default — anyone who discovers the bot's username can message it. To lock down access:

1. **Set `allowed_users`** — add your Telegram user ID(s) to the channel config. Messages from users not in the list are silently dropped. Get your user ID by messaging [@userinfobot](https://t.me/userinfobot) on Telegram.

   Static config:
   ```yaml
   telegram:
     token: ${secret:TELEGRAM_BOT_TOKEN}
     allowed_users: [123456789]
   ```

   Dynamic channel (via `channel_create`):
   ```json
   {
     "telegram_config": {
       "token": "...",
       "allowed_users": [123456789]
     }
   }
   ```

2. **Disable bot search in BotFather** — message [@BotFather](https://t.me/BotFather) and use `/setjoingroups` → disable (prevents group adds) and `/setprivacy` → enable (restricts message access in groups). This is security-through-obscurity but reduces casual discovery.

A warning is logged at startup if a Telegram channel has no `allowed_users` configured.

### Environment Filtering

Each channel can specify which environments it's active in via the `envs` field. A channel with `envs: [prod]` only starts in production. Empty `envs` means active everywhere.

### Roles

Roles are named presets of tool permissions — a simpler alternative to listing individual tools. Three roles are available:

| Role | Tools included |
|------|----------------|
| `superuser` | All Claude Code tools, all tclaw MCP tools (`mcp__tclaw__*`), all builtins, provider tools for channel connections, remote MCP tool patterns for channel remote MCPs |
| `developer` | Bash, file tools, web tools, Agent, LSP, all builtins, dev workflow tools (`mcp__tclaw__dev_*`, `mcp__tclaw__deploy`), schedule tools, model tools |
| `assistant` | File tools (Read, Edit, Write, Glob, Grep), web tools, basic builtins (stop, compact, session reset, memories reset), connection/remote MCP/schedule/model management tools, TfL tools, provider tools for channel connections, remote MCP tool patterns for channel remote MCPs |

**How roles work:**

- Roles and `allowed_tools` are **mutually exclusive** — set one or the other, never both. Setting a role clears any explicit tool list, and vice versa.
- Roles can be set at the **user level** (default for all channels) or **per-channel** (overrides the user-level setting).
- Role resolution is **dynamic** — the `superuser` and `assistant` roles include provider-specific tool patterns (e.g. `mcp__tclaw__google_*`) only when a connection exists on that channel, and remote MCP tool patterns (e.g. `mcp__linear__*`) only when a remote MCP is scoped to that channel.

**Config example (static):**

```yaml
users:
  - id: myuser
    role: superuser           # default for all channels
    channels:
      - name: admin
        type: telegram
        role: superuser       # inherits from user, but can be overridden
      - name: assistant
        type: telegram
        role: assistant       # restricted tool set
```

### Per-Channel Tool Permissions

Channels can override the user-level `allowed_tools` and `disallowed_tools` to restrict or customize what the agent can do on each channel. Alternatively, channels can use [roles](#roles) as a simpler preset-based approach.

**How it works:**

- Each channel can define `allowed_tools` and/or `disallowed_tools` lists, **or** a `role` — but not both `role` and `allowed_tools`.
- `disallowed_tools` works alongside both `role` and `allowed_tools` for surgical removal of specific tools.
- When a channel sets tool permissions (via role or explicit lists), they **replace** the user-level defaults entirely — there is no merging. This gives full control over what's available per channel.
- When a channel has no tool overrides, the user-level `allowed_tools`, `disallowed_tools`, or `role` apply as before.

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

The reset menu adapts dynamically — only shows levels allowed on the current channel.

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

### Channel Setup Patterns

There are two approaches to setting up channels, each suited to different deployment scenarios.

#### Approach 1: Admin + Dynamic Assistant (recommended for power users)

The admin channel is defined statically in config with full tool access including `channel_create`. The admin channel then creates additional channels at runtime via `channel_create` with a restricted role.

**Setup flow:**
1. Static admin channel in config with `role: superuser`
2. User messages admin channel: "set up an assistant channel on Telegram"
3. Agent guides user through @BotFather token creation
4. Agent calls `channel_create` with `role: assistant`
5. Agent restarts automatically, new channel is live

#### Approach 2: All Static Channels (recommended for managed deployments)

All channels are defined in the config file. Simpler, more predictable. No risk of accidentally deleting a channel via a tool call.

**Example config:**
```yaml
channels:
  - name: admin
    type: telegram
    description: Primary admin channel
    telegram:
      token: ${secret:TELEGRAM_ADMIN_TOKEN}
      allowed_users: [123456789]
    role: superuser
  - name: assistant
    type: telegram
    description: Mobile assistant — concise responses, no channel management
    telegram:
      token: ${secret:TELEGRAM_ASSISTANT_TOKEN}
      allowed_users: [123456789]
    role: assistant
```

| Consideration | Dynamic (Approach 1) | Static (Approach 2) |
|---------------|---------------------|---------------------|
| Adding/removing channels | Conversational, no deploy | Requires config change + deploy |
| Iterating tool permissions | `channel_edit` at runtime | Config change + deploy |
| Risk of accidental deletion | Possible via `channel_delete` | Not possible (static) |
| Best for | Power users, single-user setups | Managed/team deployments |

## Memory System

Per-user data is split into four zones with clear access boundaries (see also `docs/architecture.md` for the full directory layout):

1. **Agent memory** (`<user>/memory/`) — the agent reads and writes freely. This is the subprocess CWD and is also passed via `--add-dir`. Contains `CLAUDE.md` (the real file) and topic subfiles.
2. **Claude Code state** (`<user>/home/.claude/`) — internal CLI state (conversation history, settings, plans). Off limits to the agent. A symlink at `home/.claude/CLAUDE.md` → `../../memory/CLAUDE.md` bridges the CLI's auto-load with the agent's sandbox.
3. **tclaw state** (`<user>/state/`, `sessions/`, `secrets/`) — not mounted in the sandbox. Accessible only via MCP tools.
4. **MCP config** (`<user>/mcp-config/`) — mounted read-only in the sandbox so the CLI can read `--mcp-config`. Contains only generated MCP config JSON files (no secrets or user data).

Each user's `CLAUDE.md` is seeded on first startup with a template (`agent.DefaultMemoryTemplate`). The agent can update it to remember things across sessions. Topic-specific files can be created and referenced using `@filename.md` syntax.

## System Prompt

The system prompt is the **single source of truth** for how the agent behaves at runtime. It's built from a Go template (`agent/system_prompt.md`) and includes:

- Agent identity and behavioral rules
- Today's date
- A list of all channels with their names, types, descriptions, and sources
- The currently active channel (appended per-turn)
- Instructions for using tools, formatting messages, managing memory, and handling connections/schedules/dev workflow
- User-defined custom prompt (from config `system_prompt` field)

When adding or changing agent-facing behavior, update `agent/system_prompt.md` — not this file.

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

Users can connect external services through OAuth flows managed via MCP tools. Every connection is scoped to a specific channel so that provider tools (e.g. `google_*`) are only available on that channel.

OAuth callbacks are handled by the HTTP server at `/oauth/callback`. The callback server includes rate limiting per state code to prevent brute-force attacks.

### Google Workspace

When a Google connection is established, provider-specific MCP tools are registered: `google_gmail_list`, `google_gmail_read`, `google_workspace`, `google_workspace_schema`. Tools are only registered when a connection exists and removed when the last connection is disconnected. Services available depend on the OAuth scopes granted during consent. See the system prompt for detailed tool usage guidance.

### Monzo

When a Monzo connection is established, tools are registered: `monzo_list_accounts`, `monzo_get_balance`, `monzo_list_pots`, `monzo_list_transactions`, `monzo_get_transaction`.

**Setup:** Create a Monzo API client at [developers.monzo.com](https://developers.monzo.com/) (personal use only). Add the client ID and secret to the config:

```yaml
providers:
  monzo:
    client_id: ${secret:MONZO_CLIENT_ID}
    client_secret: ${secret:MONZO_CLIENT_SECRET}
```

The redirect URI must be set to your tclaw's OAuth callback URL (e.g. `https://your-app.fly.dev/oauth/callback` for production, `http://localhost:9876/oauth/callback` for local). Monzo uses Strong Customer Authentication — after the browser flow, the user must approve access via the Monzo app.

### TfL (Transport for London)

TfL tools are always registered — the API works without a key (rate-limited to ~50 req/min) but an API key raises the limit to ~500 req/min. Register for a free key at [api-portal.tfl.gov.uk](https://api-portal.tfl.gov.uk/products).

Tools: `tfl_line_status`, `tfl_journey`, `tfl_arrivals`, `tfl_stop_search`, `tfl_disruptions`, `tfl_road_status`.

**API key setup:** The key is stored per-user in the encrypted secret store (same pattern as GitHub/Fly tokens). Three ways to set it up:

1. **Via any tool call** — pass `api_key` as a parameter on any TfL tool. It's stored encrypted and used for all future calls.
2. **Via Telegram** — the agent prompts for the key if rate-limited.
3. **Pre-provisioned via Fly secret** — `fly secrets set TFL_API_KEY_<USER>=<key> -a tclaw`. Seeded into the encrypted store on boot.

## Remote MCP Servers

Users can connect to external MCP servers (like the Anthropic MCP directory) via MCP tools. Every remote MCP is scoped to a specific channel — its tools are only included in that channel's MCP configuration.

Remote MCPs are included in the `mcp-config.json` file that's passed to the Claude CLI via `--mcp-config`. Bearer tokens from OAuth are automatically included.

### Security

The MCP discovery client (`mcp/discovery/safeclient.go`) validates that remote MCP URLs:

- Use HTTPS (not HTTP)
- Don't resolve to private IP ranges (RFC 1918, loopback, link-local, CGN)
- Don't resolve to IPv6 loopback or unspecified addresses

## Scheduling

The agent can create and manage cron schedules that fire autonomously. When a schedule fires, it injects a message into the specified channel, waking the agent if idle. Schedules persist across agent restarts and are managed by a background goroutine that runs at user lifetime (not agent lifetime).

## Model Management

The model can be viewed and changed at runtime via MCP tools without restarting the agent:

| Tool | Purpose |
|------|---------|
| `model_list` | List all available models with short names and full IDs |
| `model_get` | Get the currently configured model |
| `model_set` | Change the model (takes effect on the next turn) |

By default, no `--model` flag is passed to the CLI, allowing it to auto-select based on the user's subscription ("auto" mode). Users can set a specific model using short names (e.g. `opus-4.6`, `sonnet-4.6`) or full model IDs. The override is stored in the user's state store and persists across agent restarts.

The config file's `model` field sets a default — the runtime override (via `model_set`) takes precedence when set.

## Dev Workflow

The agent can manage code changes, PRs, and deployments through a dev session lifecycle built on git worktrees. Multiple concurrent sessions are supported.

### Session lifecycle

```
dev_start → make changes → dev_end (commit + push + PR + cleanup)
                         → dev_cancel (discard + cleanup)

PR feedback: dev_start --branch=existing → make changes → dev_end
```

### Git authentication

GitHub PAT stored in the encrypted secret store (key: `github_token`). On first `dev_start`, the agent asks the user for a token if none is stored.

### Worktree access

Active worktree directories are passed to the Claude subprocess via `--add-dir` flags and added to the bwrap sandbox's read-write paths (Linux). On macOS (local dev), the agent can access worktrees immediately. On Linux (production), a restart is needed after `dev_start` so the sandbox picks up the new paths.

### Application logs

The `dev_logs` tool provides access to tclaw's own application logs from the running instance. Logs are captured in an in-process ring buffer (5000 lines) that tees slog output alongside stderr.

**Multi-user isolation:** Logs are filtered by the calling user's ID — each user only sees log lines tagged with their `user=<id>` field. System-wide logs (startup, shutdown, HTTP server) are hidden by default and can be included with `include_system: true`. The user ID is injected server-side from the router's config, not from user input.

**Filters:** level (DEBUG/INFO/WARN/ERROR), substring search, max line count. Defaults to 100 most recent lines at all levels.

**Scope:** Only captures logs from the current instance since boot. Previous instance logs (before a restart/deploy) are not available — use `fly logs` from the CLI for those.

### System prompt integration

Active dev sessions are listed in the system prompt so the agent knows which worktrees are available. The system prompt instructs the agent to read the project's documentation (CLAUDE.md, `@`-referenced files) from the worktree before making any code changes.

## Repo Exploration

The agent can monitor arbitrary remote git repositories for changes via `repo_*` MCP tools. This is read-only — for making changes to tclaw itself, use the dev workflow.

### Tools

| Tool | Purpose |
|------|---------|
| `repo_add` | Register a repo by name and URL |
| `repo_sync` | Fetch latest, report new commits since last check, update checkout |
| `repo_log` | Show detailed commit history with optional diffstat |
| `repo_list` | List all tracked repos and their status |
| `repo_remove` | Stop tracking and clean up all cached data |

### Storage

- **Bare repo cache** at `<userDir>/repos/<name>/bare/` — shallow single-branch clone for efficiency
- **Read-only checkout** at `<userDir>/repos/<name>/checkout/` — detached worktree for file exploration via Read/Grep/Glob
- **Tracking state** in the user's state store under `"tracked_repos"` — name, URL, branch, last-seen commit SHA, timestamps

### Lifecycle

1. `repo_add` registers metadata and creates directories (no network I/O)
2. `repo_sync` does the actual clone/fetch, updates the checkout, and advances the last-seen commit cursor
3. The agent explores files directly using Read/Grep/Glob/Bash on the checkout path
4. `repo_remove` deletes all cached data (bare repo, checkout, store entry)

No automatic cleanup or TTL — the agent manages lifecycle explicitly. Combine with `schedule_create` for periodic monitoring (e.g. daily sync + summarize).

### Authentication

Reuses the same `github_token` from the encrypted secret store as the dev workflow. Public repos work without a token. Private repos fail with a clear error if no token is available.

### Sandbox access

The `<userDir>/repos/` parent directory is pre-mounted in the bwrap sandbox, so new repo checkouts created via `repo_sync` are immediately accessible on the next turn without an agent restart.

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
- `github_token` — GitHub PAT for dev workflow (push, PR creation)
- `fly_api_token` — Fly.io API token for deploys
- `tfl_api_key` — TfL API key for Transport for London tools
- `conn/<provider>/<id>` — OAuth connection credentials (auto-refreshed)
- `channel/<name>/token` — dynamic channel secrets (lifecycle tied to channel CRUD)

### Keychain tool

The `cmd/secret` binary provides:

- `secret set <name> <value>` — store in keychain
- `secret get <name>` — retrieve from keychain
- `secret delete <name>` — delete from keychain
- `secret deploy-secrets <config> [app]` — scan config for `${secret:NAME}` refs, read each from keychain, push to Fly.io

## Oneshot Mode

`tclaw oneshot` sends a single message, prints the response, and exits. Useful for quick local testing without deploying or running the full server.

```
tclaw oneshot [flags] <message>
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `tclaw.yaml` | Path to config file |
| `--env` | `local` | Environment to load from config |
| `--user` | (first user) | User ID from config |
| `--telegram` | `false` | Emulate Telegram formatting (split messages, HTML, expandable blockquotes) |
| `--debug` | `false` | Log raw CLI event JSON |

**Examples:**

```bash
# Quick test — clean response only (logs suppressed)
tclaw oneshot "what is 2+2?" 2>/dev/null

# See full logs + response together
tclaw oneshot "check my schedules" 2>&1

# Test Telegram formatting — shows exact send/edit pattern with labels
tclaw oneshot --telegram "tell me a joke" 2>&1

# Debug raw CLI events
tclaw oneshot --debug "hello" 2>&1
```

**How it works:**

- Reuses the same per-user directories as `tclaw serve` (memory, credentials, settings)
- Starts a fresh session each time (no `--resume`)
- In normal mode, output is streamed as deltas for clean display
- In `--telegram` mode, every `Send` and `Edit` is printed verbatim with `[send msg-N]` / `[edit msg-N]` labels on stderr, so you can trace the exact message pattern that Telegram would receive
- The agent exits after one turn — no idle timeout wait

## TUI Chat Client

The `cmd/chat` binary is a Bubbletea-based terminal UI that connects to the agent via unix socket:

- Input area with multi-line support (Shift+Enter for newlines, Enter to send)
- Scrollable message history with timestamps
- All builtin commands (`reset`, `compact`, `stop`, etc.) are sent to the agent — the chat client no longer intercepts them
- Visual separation between user and assistant messages
- Auto-reconnect on socket connection
- Shows streaming output in real time via edit-in-place

## Onboarding

New users are guided through a structured onboarding flow that progressively teaches features. State is tracked in the user's state store (`onboarding` key) and persisted across agent restarts.

### Phases

| Phase | Description |
|-------|-------------|
| `welcome` | First interaction ever. Agent sends a brief welcome and asks for the user's name. |
| `info_gathering` | Conversational collection of preferences: name, home/work locations, timezone. All optional — the agent moves on if the user skips. Info is stored in CLAUDE.md and progress tracked via `onboarding_set_info`. |
| `tips_active` | A daily cron schedule (10:00 AM) fires tip prompts. The agent generates tip content on the fly from a set of feature areas, tailored to what it knows about the user. Feature area IDs are tracked to prevent repeats. |
| `complete` | All tips delivered (or user skipped). Tips schedule auto-deleted. |

### MCP Tools

| Tool | Purpose |
|------|---------|
| `onboarding_status` | Read current phase, info gathered, tips shown/remaining |
| `onboarding_set_info` | Record that a piece of user info was collected |
| `onboarding_advance` | Move to the next phase. Creates tips schedule when entering `tips_active`. |
| `onboarding_tip_shown` | Record a delivered tip. Auto-completes onboarding when all tips are done. |
| `onboarding_skip` | Skip onboarding entirely — marks complete immediately. |

### Implementation

- **State store**: `onboarding.Store` wraps the user's `store.Store`, persisted as JSON under the `"onboarding"` key.
- **System prompt injection**: The system prompt includes an `# Onboarding` section (only when incomplete) with phase-specific instructions for the agent.
- **Tips schedule**: Created by `onboarding_advance` when entering `tips_active`. Uses the existing schedule system — the tips prompt tells the agent to check `onboarding_status` and generate a personalized tip for the next uncovered feature area.
- **Reset behavior**: `ResetAll` clears the state store, so onboarding restarts from `welcome` on the next interaction.
- **Oneshot mode**: Onboarding is skipped (nil OnboardingInfo passed to the system prompt).

### Feature Areas (suggested order)

The agent covers these areas during the tips phase. Tip content is generated dynamically — only the area IDs are tracked to prevent repeats.

1. **memory** — Memory system (CLAUDE.md, topic files, @references)
2. **connections** — Service connections (Google Workspace, Monzo, built-in vs remote MCPs)
3. **scheduling** — Scheduled prompts (cron, recurring tasks, daily briefings)
4. **channels** — Multiple channels (Telegram bots, roles, per-channel permissions)
5. **tfl** — Transport for London (line status, journey planning, arrivals)
6. **web_search** — Web access (search, weather, news, current events)
7. **remote_mcps** — MCP ecosystem (remote servers, directory)
8. **compact_reset** — Context management (compact, reset, session management)
9. **dev_workflow** — Dev workflow (self-modification, git worktrees, deployment)

## HTTP Server

The HTTP server handles:

- **`/healthz`** — health check endpoint (returns 200)
- **`/oauth/callback`** — OAuth provider redirects
- **`/telegram/<channel_name>`** — Telegram webhook endpoints (production only)
