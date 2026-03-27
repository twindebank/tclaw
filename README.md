# tclaw

Multi-user Claude Code host. Spawns isolated `claude` CLI subprocesses with persistent memory, multi-channel communication, OAuth connections, scheduling, and MCP tool extensibility.

Self-hosted, single binary, drives the `claude` CLI directly (not the Agent SDK).

## Prerequisites

- **Go 1.26+** — [install](https://go.dev/dl/)
- **Node.js 22+** — required by the `claude` CLI
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code`
- **Claude Pro/Teams subscription** (recommended) or an **Anthropic API key**
- **macOS or Linux** — bubblewrap sandboxing is Linux-only; macOS runs unsandboxed locally

## Quick Start

Get a local agent running in under a minute:

```bash
# Clone and install
git clone https://github.com/twindebank/tclaw.git
cd tclaw
make install-dev
source ~/.zshrc

# Interactive setup — creates config and walks through auth
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

## What You Can Do Locally

Out of the box with just the socket channel, your agent can:

- **Remember things** — ask it to remember preferences, it writes to its `CLAUDE.md` memory file
- **Use all Claude Code tools** — Bash, file read/write/edit, web search, web fetch, etc.
- **Create schedules** — "remind me every morning at 9am to check the weather"
- **Manage its own model** — "switch to opus" / "use sonnet"
- **Monitor git repos** — "track https://github.com/org/repo and tell me about new commits"
- **Self-modify** — the agent can work on its own codebase via git worktrees (dev workflow)

Type `reset` to see reset options, `stop` to cancel a turn, `compact` to compress context.

## Adding Telegram

To access your agent from your phone, add a Telegram channel. You can do this two ways:

### Option A: Ask the agent (dynamic channel)

With the `superuser` role, just tell your agent:

> "Set up a Telegram channel for me"

It will walk you through creating a bot with [@BotFather](https://t.me/BotFather) and configure everything automatically.

### Option B: Static config

1. Create a bot with [@BotFather](https://t.me/BotFather) on Telegram — copy the token
2. Get your Telegram user ID from [@userinfobot](https://t.me/userinfobot)
3. Store the token: `tclaw secret set TELEGRAM_BOT_TOKEN <token>`
4. Update `tclaw.yaml`:

```yaml
channels:
  - type: socket
    name: main
    description: Desktop workstation

  - type: telegram
    name: mobile
    description: Mobile assistant
    role: assistant          # restricted tools — no Bash, no dev workflow
    telegram:
      token: ${secret:TELEGRAM_BOT_TOKEN}
      allowed_users: [123456789]  # your Telegram user ID
```

5. Restart the server — the bot starts polling immediately

Each channel gets its own session, and you can give different channels different roles (`superuser`, `developer`, `assistant`) to control what tools are available.

## Connections

tclaw can connect to external services via OAuth. Connections are scoped per-channel — the tools only appear on the channel where you connected.

### Google Workspace (Gmail, Calendar, Drive, Docs, Sheets)

Requires a Google Cloud OAuth client:

1. Create an OAuth 2.0 Client ID in [Google Cloud Console](https://console.cloud.google.com/apis/credentials)
2. Set the redirect URI to `http://localhost:9876/oauth/callback` (local) or `https://your-app.fly.dev/oauth/callback` (deployed)
3. Enable the APIs you want (Gmail, Calendar, Drive, etc.)
4. Install the [gws CLI](https://github.com/nicholasgasior/gws) — tclaw uses it to query Google APIs
5. Store your credentials and add the provider to config:

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

6. Tell the agent: "connect to Google" — it generates an OAuth URL, you authorize in your browser, and the tools appear: `google_gmail_list`, `google_gmail_read`, `google_workspace`, `google_workspace_schema`.

### Monzo (Banking)

1. Create an API client at [developers.monzo.com](https://developers.monzo.com/) (personal use only)
2. Set the redirect URI to your callback URL (same as Google above)
3. Store credentials and add to config:

```bash
tclaw secret set MONZO_CLIENT_ID <client-id>
tclaw secret set MONZO_CLIENT_SECRET <client-secret>
```

```yaml
providers:
  monzo:
    client_id: ${secret:MONZO_CLIENT_ID}
    client_secret: ${secret:MONZO_CLIENT_SECRET}
```

4. Tell the agent: "connect to Monzo" — after browser auth, you also need to approve access in the Monzo app (Strong Customer Authentication). Tools: `monzo_list_accounts`, `monzo_get_balance`, `monzo_list_pots`, `monzo_list_transactions`.

### TfL (Transport for London)

TfL tools work immediately with no setup — the API is free and keyless (rate-limited to ~50 req/min). For higher limits (~500 req/min), register for a free key at [api-portal.tfl.gov.uk](https://api-portal.tfl.gov.uk/products) and pass it to any TfL tool call — the agent stores it automatically.

Tools: `tfl_line_status`, `tfl_journey`, `tfl_arrivals`, `tfl_stop_search`, `tfl_disruptions`, `tfl_road_status`.

### Remote MCP Servers

The agent can also connect to any remote MCP server (like those in the [Anthropic MCP directory](https://www.anthropic.com/marketplace)):

> "Connect to the Linear MCP server at https://mcp.linear.app/sse"

The agent handles OAuth discovery and registration automatically. Remote MCP connections are scoped per-channel, and URLs are validated against SSRF (HTTPS required, private IPs blocked).

## Secrets & Credentials

tclaw manages secrets at three levels:

### 1. Config-time secrets (OS keychain)

For values referenced in `tclaw.yaml` via `${secret:NAME}`:

```bash
tclaw secret set GOOGLE_CLIENT_ID <value>
tclaw secret set TELEGRAM_BOT_TOKEN <value>
```

tclaw tries the keychain first, falls back to env vars, then scrubs the env var from the process so subprocesses can't read it.

**When to use:** Provider credentials (Google, Monzo), Telegram bot tokens, API keys — anything your config file references.

### 2. Runtime secrets (encrypted store)

Secrets the agent collects during conversation (OAuth tokens, GitHub PATs, TfL API keys) are stored in a per-user NaCl-encrypted store. These persist across restarts.

**When to use:** Managed automatically. The agent asks for credentials when tools need them and stores them encrypted via secure web forms.

### 3. Production secrets (Fly.io)

For deployed instances, secrets flow from keychain to Fly:

```bash
# Push all config-referenced secrets at once
tclaw deploy secrets

# Per-user tool secrets use a naming convention
fly secrets set GITHUB_TOKEN_MYUSER=ghp_... -a your-app
fly secrets set FLY_TOKEN_MYUSER=... -a your-app
```

On boot, tclaw reads per-user env vars (`<PREFIX>_<USER>`) and seeds them into the encrypted store. The env vars are then scrubbed — the claude subprocess never sees them.

**When to use:** Only when deploying to Fly.io. `tclaw deploy secrets` handles config secrets; per-user secrets need manual `fly secrets set`.

## Going to Production

Once you're happy locally, deploy to Fly.io for always-on access via Telegram. This is entirely optional — tclaw works great as a local-only tool.

> **Tip:** Use the `/deploy-to-prod` Claude Code skill for guided, interactive setup that auto-generates your prod config.

The manual steps:

```bash
# 1. Install Fly CLI and log in
brew install flyctl
fly auth login

# 2. Create the app and volume
fly apps create your-app-name
fly volumes create tclaw_data --region lhr --size 1 -a your-app-name -y

# 3. Add a prod section to tclaw.yaml
# 4. Update fly.toml with your app name
# 5. Push secrets and deploy
tclaw deploy secrets
tclaw deploy
```

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
      role: superuser
      channels:
        - type: telegram
          name: mobile
          description: Mobile assistant
          role: assistant
          telegram:
            token: ${secret:TELEGRAM_BOT_TOKEN}
            allowed_users: [123456789]
```

Set the OAuth callback URL in your provider consoles to `https://your-app.fly.dev/oauth/callback`.

See [docs/deployment.md](docs/deployment.md) for memory tuning, suspend/resume, and CI setup.

## Commands

| Command | Description |
|---------|-------------|
| `tclaw init` | Interactive setup — create config and authenticate |
| `tclaw serve` | Start the agent server |
| `tclaw serve --dev` | Hot-reload server (restarts on `.go` changes, requires `air`) |
| `tclaw chat` | Connect the TUI chat client |
| `tclaw oneshot "msg"` | Send a single message and exit |
| `tclaw secret set NAME value` | Store a secret in the OS keychain |
| `tclaw secret get NAME` | Retrieve a secret |
| `tclaw deploy` | Build and deploy to Fly.io |
| `tclaw deploy secrets` | Push keychain secrets to Fly.io |
| `tclaw deploy logs` | Tail production logs |

## Roles

Roles control what tools the agent can use, per-user or per-channel:

| Role | What it can do |
|------|----------------|
| `superuser` | Everything — all Claude Code tools, all MCP tools, Bash, dev workflow, deployment, channel management |
| `developer` | Code-focused — Bash, file tools, dev workflow, deployment, scheduling. No connections or provider tools. |
| `assistant` | Safe for mobile — file tools, web search, connections, scheduling, TfL. No Bash, no dev workflow. |

```yaml
users:
  - id: myuser
    role: superuser            # default for all channels
    channels:
      - name: desktop
        type: socket           # inherits superuser
      - name: phone
        type: telegram
        role: assistant        # override for this channel
```

## Documentation

- **[Features](docs/features.md)** — comprehensive feature reference: channels, memory, auth, connections, scheduling, dev workflow, remote MCPs, etc.
- **[Architecture](docs/architecture.md)** — package map, dependency layers, data flows, security model, directory layout.
- **[Deployment](docs/deployment.md)** — Fly.io setup, secrets, memory tuning, CI.
- **[Contributing](CONTRIBUTING.md)** — development setup, tests, code style.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

[MIT](LICENSE)
