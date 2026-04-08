# tclaw

[![CI](https://github.com/twindebank/tclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/twindebank/tclaw/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/twindebank/tclaw)](https://goreportcard.com/report/github.com/twindebank/tclaw)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A self-hosted personal AI assistant that wraps the real `claude` CLI -- not an Agent SDK. This means you get everything Claude Code already has (Bash, file tools, web search, model switching, context compaction) without reimplementing any of it. tclaw adds multi-channel communication, per-channel tool groups, OAuth connections, scheduling, and a full MCP tool layer on top.

Single Go binary. No containers required to run.

## How It Works

```
  Channels (socket, Telegram, stdio)
              |
        +-----v------+
        |   Router    |  per-user lifecycle, lazy start/stop
        +-----+------+
              |
     +--------v---------+
     |  claude CLI       |  spawned per turn, stream-json output
     |  (real Claude Code)|  all built-in tools available
     +--------+----------+
              |
     +--------v---------+
     |  MCP Server       |  tclaw's own tools: channels, schedules,
     |  localhost:<random>|  connections, secrets, dev workflow, etc.
     +-------------------+
```

tclaw spawns the actual `claude` CLI binary per conversation turn, streaming structured JSON events back to the channel. Each user gets their own isolated subprocess with a separate HOME directory, memory files, encrypted secret store, and MCP server. The agent starts lazily on the first message and shuts down after 10 minutes of inactivity.

## Key Features

**Multi-channel with per-channel tool groups** -- Socket (local TUI), Telegram (mobile), stdio. Each channel gets its own session and its own set of tool groups: full CLI tools on desktop, safe-only tools on mobile.

**Agent-managed channels** -- The agent can create, edit, and delete channels at runtime via MCP tools. Ephemeral channels auto-provision Telegram bots and clean up after an idle timeout. Config is the source of truth -- a reconciler auto-converges desired state on every change.

**OAuth connections** -- Google Workspace, Monzo, Enable Banking, and more. Connections are scoped per-channel with automatic token refresh. The agent walks users through the OAuth flow.

**Remote MCP servers** -- Connect to any MCP server (Linear, etc.) with automatic OAuth discovery and registration. Scoped per-channel.

**Scheduling and notifications** -- Cron schedules, Gmail polling, push notifications. All message sources (user, schedule, notification, cross-channel) flow through a unified priority queue -- user messages always come first, everything else waits for idle.

**Dev workflow** -- The agent can work on its own codebase: git worktrees, PR creation, deployment, log inspection. It can also monitor external repos for new commits.

**Persistent memory** -- Per-user `CLAUDE.md` and topic files. The agent reads and writes its own memory, carried across sessions and restarts.

**Security** -- Per-user filesystem sandbox via bubblewrap (Linux), environment allowlist (no cloud credentials leak to subprocess), NaCl-encrypted secret store with three-tier resolution (keychain, encrypted store, Fly secrets), env var scrubbing before subprocess spawn.

## Prerequisites

- **Go 1.26+** -- [install](https://go.dev/dl/)
- **Node.js 22+** -- required by the `claude` CLI
- **Claude Code CLI** -- `npm install -g @anthropic-ai/claude-code`
- **Claude Pro/Teams subscription** (recommended) or an **Anthropic API key**
- **macOS or Linux** -- bubblewrap sandboxing is Linux-only; macOS runs unsandboxed locally

## Quick Start

Get a local agent running in under a minute:

```bash
# Clone and install
git clone https://github.com/twindebank/tclaw.git
cd tclaw
make install-dev
source ~/.zshrc

# Interactive setup -- creates config and walks through auth
tclaw init

# Start the server (terminal 1)
tclaw serve

# Connect the chat client (terminal 2)
tclaw chat
```

`tclaw init` checks prerequisites, creates `tclaw.yaml`, and optionally authenticates (Claude OAuth via browser, or API key). If you skip auth during init, the agent walks you through it on your first message.

For a quick smoke test without the TUI:

```bash
tclaw oneshot "hello, what can you do?" 2>/dev/null
```

> **Tip:** See `tclaw.example.yaml` for a fully commented config reference.

## What You Can Do Out of the Box

With just the socket channel, your agent can:

- **Remember things** -- ask it to remember preferences, it writes to its `CLAUDE.md` memory file
- **Use all Claude Code tools** -- Bash, file read/write/edit, web search, web fetch, etc.
- **Create schedules** -- "remind me every morning at 9am to check the weather"
- **Manage its own model** -- "switch to opus" / "use sonnet"
- **Monitor git repos** -- "track https://github.com/org/repo and tell me about new commits"
- **Self-modify** -- the agent can work on its own codebase via git worktrees (dev workflow)

Type `reset` to see reset options, `stop` to cancel a turn, `compact` to compress context.

## Adding Telegram

To access your agent from your phone, add a Telegram channel. You can do this two ways:

### Option A: Ask the agent

Tell your agent:

> "Set up a Telegram channel for me"

It will walk you through creating a bot with [@BotFather](https://t.me/BotFather) and configure everything automatically.

### Option B: Add to config

1. Create a bot with [@BotFather](https://t.me/BotFather) on Telegram -- copy the token
2. Get your Telegram user ID from [@userinfobot](https://t.me/userinfobot)
3. Store the token: `tclaw secret set TELEGRAM_BOT_TOKEN <token>`
4. Update `tclaw.yaml`:

```yaml
channels:
  - type: socket
    name: main
    description: Desktop workstation
    tool_groups:
      - core_tools
      - all_builtins
      - channel_management
      - dev_workflow
      - scheduling

  - type: telegram
    name: mobile
    description: Mobile assistant
    tool_groups:
      - safe_builtins       # stop, compact, session/memory reset only
      - channel_messaging
      - scheduling
      - personal_services
    telegram:
      token: ${secret:TELEGRAM_BOT_TOKEN}
      allowed_users: [123456789]  # your Telegram user ID
```

5. Restart the server -- the bot starts polling immediately

Each channel gets its own session. Tool groups are additive -- channels start with nothing and you add what they need. Use `tool_group_list` (via the agent) to see all available groups with descriptions.

## Connections

tclaw can connect to external services via OAuth. Connections are scoped per-channel -- the tools only appear on the channel where you connected.

### Google Workspace (Gmail, Calendar, Drive, Docs, Sheets)

1. Create an OAuth 2.0 Client ID in [Google Cloud Console](https://console.cloud.google.com/apis/credentials) and enable the APIs you want
2. Set the redirect URI to `http://localhost:9876/oauth/callback` (local) or `https://your-app.fly.dev/oauth/callback` (deployed)
3. Install the [gws CLI](https://github.com/nicholasgasior/gws)
4. Store credentials and add the provider to config:

```bash
tclaw secret set GOOGLE_CLIENT_ID <client-id>
tclaw secret set GOOGLE_CLIENT_SECRET <client-secret>
```

```yaml
providers:
  google:
    client_id: ${secret:GOOGLE_CLIENT_ID}
    client_secret: ${secret:GOOGLE_CLIENT_SECRET}
```

5. Tell the agent: "connect to Google" -- it handles the OAuth flow

### Other Providers

The agent surfaces setup details via tool descriptions at runtime. Tell it to connect and it will guide you through each provider's requirements.

- **Monzo** -- banking (balances, transactions, pots). Create an API client at [developers.monzo.com](https://developers.monzo.com/), add client ID/secret to config under `providers.monzo`.
- **TfL** -- Transport for London (line status, journeys, arrivals, disruptions). Works immediately with no setup. Optional API key from [api-portal.tfl.gov.uk](https://api-portal.tfl.gov.uk/products) for higher rate limits.
- **Enable Banking** -- Open Banking (PSD2) account access. Register at [enablebanking.com](https://enablebanking.com/).
- **Resy** -- restaurant search and booking. Credentials from browser dev tools.

### Remote MCP Servers

The agent can also connect to any remote MCP server (like those in the [Anthropic MCP directory](https://www.anthropic.com/marketplace)):

> "Connect to the Linear MCP server at https://mcp.linear.app/sse"

The agent handles OAuth discovery and registration automatically. Remote MCP connections are scoped per-channel, and URLs are validated against SSRF (HTTPS required, private IPs blocked).

## Secrets & Credentials

tclaw manages secrets at three levels:

### 1. Config-time secrets (OS keychain)

For values referenced in `tclaw.yaml` via `${secret:NAME}`. These are resolved at startup before anything runs.

```bash
tclaw secret set GOOGLE_CLIENT_ID <value>
tclaw secret set TELEGRAM_BOT_TOKEN <value>
```

tclaw tries the keychain first, falls back to env vars, then scrubs the env var from the process so subprocesses can't read it.

**When to use:** Provider credentials (Google, Monzo), Telegram bot tokens, API keys -- anything your config file references at boot time.

### 2. Runtime secrets (encrypted store)

Secrets the agent collects during conversation (OAuth tokens, GitHub PATs, TfL API keys) are stored in a per-user NaCl-encrypted store. These persist across restarts.

**When to use:** Managed automatically. The agent asks for credentials when tools need them and stores them encrypted via secure web forms. No keychain access needed -- this is how deployed instances handle credentials without a desktop OS.

### 3. Production secrets (Fly.io)

For deployed instances where there is no OS keychain, secrets flow from your local machine to Fly:

```bash
# Push all config-referenced secrets at once
tclaw deploy secrets

# Per-user tool secrets use a naming convention
fly secrets set GITHUB_TOKEN_MYUSER=ghp_... -a your-app
fly secrets set FLY_TOKEN_MYUSER=... -a your-app
```

On boot, tclaw reads per-user env vars (`<PREFIX>_<USER>`) and seeds them into the encrypted store. The env vars are then scrubbed -- the claude subprocess never sees them.

**When to use:** Only when deploying to Fly.io. `tclaw deploy secrets` handles config secrets; per-user secrets need manual `fly secrets set`.

## Going to Production

Once you're happy locally, deploy to Fly.io for always-on access via Telegram. This is entirely optional -- tclaw works great as a local-only tool.

> **Tip:** Use the `/deploy-to-prod` Claude Code skill for guided, interactive setup that auto-generates your prod config.

The manual steps:

```bash
# 1. Install Fly CLI and log in
brew install flyctl
fly auth login

# 2. Create the app and volume
fly apps create your-app-name
fly volumes create tclaw_data --region lhr --size 1 -a your-app-name -y

# 3. Create fly.toml from the example
cp fly.example.toml fly.toml
# Edit fly.toml — set your app name

# 4. Add a prod section to tclaw.yaml
# 5. Push secrets and deploy
tclaw deploy secrets
tclaw deploy
```

> **Note:** `fly.toml` is gitignored. Run `tclaw init` or copy `fly.example.toml` to create it.

Your config file is baked into the Docker image. The `prod:` section is selected via `--env prod`:

```yaml
prod:
  base_dir: /data/tclaw
  server:
    addr: 0.0.0.0:9876
    public_url: https://your-app.fly.dev  # enables Telegram webhooks
  users:
    - id: default
      model: claude-sonnet-4-6
      permission_mode: dontAsk
      channels:
        - type: telegram
          name: mobile
          description: Mobile assistant
          tool_groups:
            - safe_builtins
            - channel_messaging
            - scheduling
            - personal_services
            - connections
          telegram:
            token: ${secret:TELEGRAM_BOT_TOKEN}
            allowed_users: [123456789]
```

Set the OAuth callback URL in your provider consoles to `https://your-app.fly.dev/oauth/callback`.

See [docs/deployment.md](docs/deployment.md) for memory tuning, suspend/resume, and CI setup.

## Commands

| Command | Description |
|---------|-------------|
| `tclaw init` | Interactive setup -- create config and authenticate |
| `tclaw serve` | Start the agent server |
| `tclaw serve --dev` | Hot-reload server (restarts on `.go` changes, requires `air`) |
| `tclaw chat` | Connect the TUI chat client |
| `tclaw oneshot "msg"` | Send a single message and exit |
| `tclaw secret set NAME value` | Store a secret in the OS keychain |
| `tclaw secret get NAME` | Retrieve a secret |
| `tclaw deploy` | Build and deploy to Fly.io |
| `tclaw deploy secrets` | Push keychain secrets to Fly.io |
| `tclaw deploy logs` | Tail production logs |

## Tool Groups

Tool groups control what the agent can do on each channel. Groups are additive -- channels start with no tools and you compose them:

| Group | What it includes |
|-------|-----------------|
| `core_tools` | Bash, file ops, web search/fetch |
| `all_builtins` | All built-in commands (stop, compact, login, auth, all reset levels) |
| `safe_builtins` | Safe commands only (stop, compact, session/memory reset) |
| `channel_messaging` | Send messages to other channels, read transcripts, check busy state |
| `channel_management` | Full channel lifecycle (create, delete, edit, list) plus messaging |
| `scheduling` | Cron schedules (create, edit, delete, pause, resume) |
| `dev_workflow` | Dev sessions, deployment, log inspection |
| `repo_monitoring` | Track external git repos for new commits |
| `gsuite_read` | Google Workspace read-only (email, calendar, docs) |
| `gsuite_write` | Google Workspace full access (includes read) |
| `personal_services` | TfL, restaurant reservations, banking, Monzo |
| `connections` | Manage OAuth and remote MCP connections |
| `telegram_client` | Telegram Client API (MTProto) for bot management |
| `notifications` | Push notifications (new emails, PR merges, etc.) |
| `onboarding` | New user onboarding flow |
| `secret_form` | Collect credentials via secure web forms |

```yaml
channels:
  - name: desktop
    type: socket
    tool_groups:
      - core_tools
      - all_builtins
      - channel_management
      - dev_workflow
      - scheduling
      - connections

  - name: phone
    type: telegram
    tool_groups:
      - safe_builtins
      - channel_messaging
      - scheduling
      - personal_services
```

## Documentation

- **[Architecture](docs/architecture.md)** -- package map, dependency layers, data flows, security model, directory layout.
- **[Deployment](docs/deployment.md)** -- Fly.io setup, secrets, memory tuning, CI.
- **[Contributing](CONTRIBUTING.md)** -- development setup, tests, code style.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

[MIT](LICENSE)
