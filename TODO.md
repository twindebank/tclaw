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
- [ ] Voice message transcription — Telegram sends voice notes as .ogg files which the agent can't read. Claude API has no native audio input (as of Mar 2026); use Groq Whisper API (generous free tier). Transcribe at ingest in the Telegram handler, inject as `[Voice message: "<transcript>"]` so the agent sees plain text with no changes needed downstream.
- [ ] Token exhausted state — when Claude API limit is hit, record reset time in channel state. Scheduled jobs should be deferred until reset time (not silently dropped). On reset, prompt user whether to run deferred schedules or skip them. Normal inbound messages should be queued and replayed (or flagged as unactioned) when the limit resets so nothing is silently lost.

## Memory & Context
- [x] System and agent memories — system prompt (--append-system-prompt) for identity/rules, CLAUDE.md for persistent per-user memory, seeded on first startup
- [x] Custom MCPs can be built in — register Go-native MCP servers alongside the agent
- [ ] TfL MCP — London transport status, journey planning, disruptions
- [ ] Calendar MCP — read/write calendar events (Google Calendar, etc.)
- [ ] Home Assistant MCP — control smart home devices, query states, trigger automations
- [ ] Google Maps / location tools — place search, directions, travel time; location history integration

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
- [ ] Channel history store — archive deleted channels (name, type, session ID, dev session, timestamps) so the agent can reference past ephemeral tasks. `channel_history` MCP tool for querying.
- [ ] Channel types in own packages — move socket/stdio/telegram/oneshot into `channel/socketchannel/` etc. with builders co-located. Currently builders are in `router/`.
- [ ] `channel_delete` cleanup — archive the chat (export/preserve history before deleting the bot) and automatically cancel any associated dev sessions. Requires channels to track their dev sessions and dev sessions to be tagged with the originating channel. (Note: `channel_done` now requires user confirmation before teardown.)
- [ ] Channel busy/free check — `channel_is_busy` tool: returns whether a channel has an active agent turn or recent conversation activity (with configurable timeout). Enables scheduled tasks to check before sending cross-channel messages, and to queue/defer if busy rather than interrupting.
- [ ] Ephemeral channel system prompt enrichment — when a channel is created as ephemeral, inject context into its system prompt: the `initial_message` (so the agent knows its task from the start, not just from the first inbound message), and a note that it is a purpose-scoped ephemeral channel (so it stays focused and knows to call `channel_done` when done).
- [ ] Error channel — dedicated channel that receives errors from the logger. Hook into `slog` (or the logbuffer) so that ERROR-level log entries are automatically posted to a designated channel. Enables the agent to see and react to runtime errors without manually checking `dev_logs`.

## Connectivity
- [x] Remote MCP support — add/remove/list remote MCP servers with OAuth discovery (RFC 7591)
- [x] OAuth connections — provider-based OAuth flow via callback server, credential encryption, per-user isolation
- [x] Google Workspace — Gmail, Drive, Calendar, Docs, Sheets, Slides, Tasks via `gws` binary

## Automation
- [x] Task scheduling — cron-like triggers that kick off agent sessions autonomously
- [x] Schedule management tools — MCP tools for create, list, edit, delete, pause, resume
- [ ] GitHub PR merged notifications — notify the relevant channel when a PR is merged. Now possible via the notification system — devtools implements `Notifier` with a webhook watcher.
- [ ] Email auto-categorisation — auto-categorise emails (e.g. receipts, travel, action-required) and apply a skill to handle each category automatically. Gmail notifications are already in place; categorisation is the next step.

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
- [ ] Email monitoring — set up BetterStack or Fly.io native email alerts for downtime/errors
- [ ] System message posted to admin channel on every redeployment (with commit hash / changelog)
- [ ] Proper versioning for deployments (semver tags, changelog generation, release notes)

## Maintenance
- [ ] Periodic jobs to check Claude Code changelog and dynamically update agent/CLI behavior
- [ ] Check CVEs and dynamically update dependencies
- [ ] Generally update dependencies (go mod tidy, bump versions)
- [ ] Other periodic maintenance tasks (e.g. rotate secrets, audit configs)
- [ ] Periodic job to inspect logs automatically and open PRs to fix recurring errors
- [ ] Periodic job to audit repo dependencies and open upgrade PRs (needs `repo_*` + `dev_*` tools working together)

## Dev Experience
- [ ] GitHub Actions CI: run `go build ./...` + `go test ./...` on every PR (currently no CI)
- [ ] `dev_logs` time range filter — `since` param (e.g. "last 4 days") so historical logs are easily queryable
- [ ] Local dev parity — `make dev` that spins up the full stack with a test Telegram bot token and hot reload
- [ ] PR preview deployments — deploy PRs to a separate Fly.io app (`tclaw-preview`) for manual testing before merge
- [ ] `dev_status` should show CI check results alongside uncommitted changes
- [ ] Structured log viewer — `dev_logs` currently returns raw text; a mode that groups by session/tool-call would help debug agent turns
- [ ] Replay harness — record + replay Telegram message sequences to test agent behavior without live bots

## Documentation
- [x] Project docs — [features](docs/features.md), [architecture](docs/architecture.md), README
- [x] Coding style guide — `docs/go-patterns.md` with comments, error handling, testing, function design, naming conventions
