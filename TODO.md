# tclaw Roadmap

## Core Agent
- [x] Agent loop — multi-turn conversations via `--resume` session continuation
- [x] Session management / persistence — session ID captured from CLI and reused across turns
- [x] Tool use — Claude Code executes tools internally; we get results in the stream
- [x] Streaming responses — send text deltas to the channel as they arrive instead of buffering
- [x] Stream thinking — surface extended thinking blocks to the channel in real time
- [x] Stream tool use — show tool calls and results in the channel as they happen
- [x] Lazy agent lifecycle — spawn agent on first message
- [x] Inactivity timeout — auto-shutdown agent after idle period, restarts on next message

## Memory & Context
- [x] System and agent memories — system prompt (--append-system-prompt) for identity/rules, CLAUDE.md for persistent per-user memory, seeded on first startup
- [x] Custom MCPs can be built in — register Go-native MCP servers alongside the agent
- [ ] TfL MCP — London transport status, journey planning, disruptions
- [ ] Calendar MCP — read/write calendar events (Google Calendar, etc.)
- [ ] Home Assistant MCP — control smart home devices, query states, trigger automations

## Permissions & Security
- [x] Secret management — OS keychain (local) + encrypted FS (deployed), per-user isolation, ${secret:NAME} config syntax
- [x] Channel descriptions — agent aware of current and other channels via config descriptions
- [x] Per-channel tool allowlists — restrict which tools are available on each channel independently, with builtin command gating via `builtin__*` names
- [ ] Clearly define session access boundaries — make it explicit what the agent can/can't access in terms of current session state vs other sessions (cross-session isolation model)
- [ ] Tool permissions / 2FA — approve or deny tool calls via the chat channel
- [ ] Privileged sessions — temporary elevated permissions with a timeout (e.g. user grants write access for 30 min)
- [ ] Permission matching rules — expressive rules for tool permissions based on provider, read/write, resource scope, etc.

## UX
- [x] Split thinking and final message — separate thinking/tool-use status from response text into distinct messages (Telegram split mode)
- [ ] Typing indicator — show typing state in the interface while agent is working
- [x] Timestamps on messages — show when each message was sent/received
- [x] Visual message separation — clearer boundaries between messages in the chat UI
- [x] Show tool arguments — display tool call parameters alongside tool use events
- [x] Chat keywords — builtin commands: `stop` (abort current response), `compact` (compact context), `new`/`reset`/`clear`/`delete` (multi-level reset menu), `login`/`auth` (interactive auth flow)
- [ ] Chat keywords (remaining) — `model` (switch model), `help` (list commands)
- [ ] Render markdown in chat — parse and render markdown formatting in the TUI client
- [ ] Web browser tool / Selenium — give the agent the ability to browse and interact with web pages

## Multi-User
- [x] User identity — user ID system for identifying distinct users
- [x] Multi-user support — isolate sessions, memory, and permissions per user
- [x] Per-user config — API key, model, permission mode, allowed/disallowed tools via YAML
- [x] Per-channel session isolation — each channel gets its own Claude session
- [x] Config validation — validate model, permission mode, tools, and channel types against known values

## Channel
- [x] Edit message — allow the channel to update/edit previously sent messages (e.g. for streaming edits in place)
- [x] Channel-specific config in system prompt — per-channel context (name, type, description) injected into the agent's system prompt
- [x] Dynamic channels — agent can create, edit, and delete channels at runtime via MCP tools
- [x] Telegram support — Bot API with long polling (local) and webhooks (production), HTML markup
- [ ] Slack support
- [ ] Signal support

## Connectivity
- [x] Remote MCP support — add/remove/list remote MCP servers with OAuth discovery (RFC 7591)
- [x] OAuth connections — provider-based OAuth flow via callback server, credential encryption, per-user isolation
- [x] Google Workspace — Gmail, Drive, Calendar, Docs, Sheets, Slides, Tasks via `gws` binary

## Automation
- [x] Task scheduling — cron-like triggers that kick off agent sessions autonomously
- [x] Schedule management tools — MCP tools for create, list, edit, delete, pause, resume

## Self-Modification
- [x] Dev tools — MCP tools for dev workflow via git worktrees:
  - `dev_start` — clones/fetches repo (bare clone), creates worktree on new/existing branch
  - `dev_status` — shows branch, uncommitted changes, commit log, diff stat
  - `dev_end` — commits, pushes, creates PR via `gh`, tears down worktree
  - `dev_cancel` — removes worktree and branch without pushing
  - `deploy` — two-phase Fly.io deploy with preview and confirmation

## Operations
- [x] Deployment — Docker + Fly.io with persistent volume, health checks, CI workflow
- [ ] Monitoring — metrics, alerting, cost tracking (logging is in place via slog)

## Documentation
- [x] Project docs — [features](docs/features.md), [architecture](docs/architecture.md), README
- [x] Coding style guide — `docs/go-patterns.md` with comments, error handling, testing, function design, naming conventions
