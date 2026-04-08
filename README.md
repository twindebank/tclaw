# tclaw

[![CI](https://github.com/twindebank/tclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/twindebank/tclaw/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/twindebank/tclaw)](https://goreportcard.com/report/github.com/twindebank/tclaw)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A self-hosted personal AI assistant built on the real `claude` CLI. Not an SDK wrapper -- tclaw spawns Claude Code directly, so you get everything it already has (Bash, file tools, web search, model switching, context compaction) without reimplementing any of it.

tclaw adds multi-channel communication, agent-managed infrastructure, OAuth connections, scheduling, and 80+ MCP tools on top. Single Go binary.

## Quick Start

### Option A: Let Claude Code do it

```bash
git clone https://github.com/twindebank/tclaw.git && cd tclaw
```

Open the repo in Claude Code and run `/quickstart`. It checks prerequisites, generates config, handles auth, and tells you what to do next.

### Option B: Manual

```bash
git clone https://github.com/twindebank/tclaw.git && cd tclaw
make install-dev && source ~/.zshrc
tclaw init       # checks prereqs, creates config, handles auth
tclaw serve      # terminal 1
tclaw chat       # terminal 2
```

That's it. The agent handles the rest -- ask it to set up Telegram, connect to Google, schedule tasks, or deploy itself to production.

**Prerequisites:** Go 1.26+, Node.js 22+, [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code/overview) (`npm i -g @anthropic-ai/claude-code`), Claude Pro/Teams or an Anthropic API key.

## What Makes tclaw Different

**The agent manages its own infrastructure.** It creates and deletes channels, edits its config, provisions Telegram bots, connects OAuth providers, and deploys itself to production -- all through conversation. You describe what you want; it handles the plumbing.

**Real Claude Code, not a reimplementation.** tclaw spawns the actual `claude` binary per conversation turn. Every Claude Code feature works out of the box -- Bash, file operations, web search, MCP servers, permission modes. tclaw just adds the layer above.

**Multi-channel with per-channel capabilities.** Run the same agent across desktop (socket), mobile (Telegram), and scheduled tasks -- each channel with its own tool permissions, memory files, and session history. A "work" channel gets dev tools and repo access; a "personal" channel gets banking and transport.

**Autonomous work sessions.** Scheduled tasks spin up ephemeral channels with scoped tools, do their work in isolation, deliver results to linked channels, and tear down. Not cron notifications -- actual agent work sessions.

**Self-modifying.** The agent can work on its own codebase: create a git worktree, make changes, open a PR, monitor CI, verify the deploy landed, and confirm it's running the new version.

**Streaming thinking in collapsible messages.** On Telegram, Claude's reasoning appears in expandable blockquotes -- you see the thinking process without it overwhelming the conversation. Tool use and results are collapsed the same way.

## What You Can Do

Everything below works through natural conversation -- just ask.

- **Persistent memory** -- remembers preferences, context, and instructions across sessions
- **All Claude Code tools** -- Bash, file read/write/edit, web search, web fetch, model switching
- **Telegram** -- *"set up a Telegram channel for me"* and it handles BotFather, config, everything
- **Google Workspace** -- Gmail, Calendar, Drive, Docs, Sheets via OAuth
- **Banking** -- Monzo, Open Banking (PSD2), balances and transactions
- **Transport** -- TfL journey planning, live arrivals, disruptions (works immediately)
- **Restaurants** -- search, availability, booking
- **Remote MCP servers** -- *"connect to the Linear MCP server"* with automatic OAuth discovery
- **Scheduling** -- not just reminders: schedules spin up ephemeral work sessions that do real work and deliver results
- **Repo monitoring** -- track external repos, get notified about new commits
- **Self-modification** -- works on its own codebase via git worktrees, opens PRs, monitors CI, verifies deploys

## Deploying to Production

For always-on Telegram access, deploy to Fly.io. The easiest way:

```bash
claude
> /deploy-to-prod
```

The skill checks prerequisites, generates your prod config, creates the Fly app, pushes secrets, and deploys. Or see [docs/deployment.md](docs/deployment.md) for manual steps.

Deploys also happen automatically via GitHub Actions CI on push to main.

## Security

- Per-user subprocess isolation via HOME env var + bubblewrap sandbox (Linux)
- Environment allowlist -- no cloud credentials, SSH agents, or tokens leak to subprocesses
- NaCl-encrypted secret store with three-tier resolution (keychain, encrypted store, Fly secrets)
- Env var scrubbing before subprocess spawn
- Per-user MCP server on localhost with random bearer token

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## Documentation

- [Architecture](docs/architecture.md) -- package map, dependency layers, security model
- [Deployment](docs/deployment.md) -- Fly.io setup, secrets, memory tuning, CI
- [Go Patterns](docs/go-patterns.md) -- code style, error handling, testing conventions
- [Contributing](CONTRIBUTING.md) -- development setup and workflow

## Claude Code Skills

| Skill | Purpose |
|-------|---------|
| `/quickstart` | Guided local setup -- checks prereqs, generates config, handles auth |
| `/deploy-to-prod` | Guided Fly.io deployment -- generates prod config, creates app, deploys |
| `/add-tool-package` | Scaffold a new MCP tool package with registration, definitions, and tests |
| `/add-channel-type` | Scaffold a new channel type with transport, package, and registry entry |
| `/troubleshoot` | Diagnose issues -- checks config, processes, logs, prerequisites |
| `/status` | Quick overview of local server, deployment, and config state |
| `/update` | Pull latest, rebuild, and optionally redeploy |

## License

[MIT](LICENSE)
