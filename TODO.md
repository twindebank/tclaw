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

## Permissions & Security
- [x] Secret management — OS keychain (local) + encrypted FS (deployed), per-user isolation, ${secret:NAME} config syntax
- [x] Channel descriptions — agent aware of current and other channels via config descriptions
- [ ] Tool permissions / 2FA — approve or deny tool calls via the chat channel
- [ ] Privileged sessions — temporary elevated permissions with a timeout (e.g. user grants write access for 30 min)
- [ ] Permission matching rules — expressive rules for tool permissions based on provider, read/write, resource scope, etc.
- [ ] Per-channel tool allowlists — restrict which tools are available on each channel independently

## UX
- [ ] Typing indicator — show typing state in the interface while agent is working
- [x] Timestamps on messages — show when each message was sent/received
- [x] Visual message separation — clearer boundaries between messages in the chat UI
- [x] Show tool arguments — display tool call parameters alongside tool use events
- [ ] Chat keywords — special commands: `/new` (reset session), `/restart` (restart agent), `/status` (show agent info), `/model` (switch model), `/compact` (compact context), `/help` (list commands), `/cancel` (abort current response)
- [ ] Render markdown in chat — parse and render markdown formatting in the TUI client
- [ ] Web browser tool / Selenium — give the agent the ability to browse and interact with web pages

## Multi-User
- [x] User identity — user ID system for identifying distinct users
- [x] Multi-user support — isolate sessions, memory, and permissions per user
- [x] Per-user config — API key, model, permission mode, allowed/disallowed tools via YAML
- [x] Per-channel session isolation — each channel gets its own Claude session
- [x] Config validation — validate model, permission mode, tools, and channel types against known values
- [ ] Filesystem-based database — lightweight persistent storage (users, sessions, etc.) backed by disk, suitable for use with persistent volumes

## Channel
- [x] Edit message — allow the channel to update/edit previously sent messages (e.g. for streaming edits in place)
- [ ] Channel-specific config in system prompt — inject per-channel context (capabilities, restrictions, description) into the agent's system prompt

## Connectivity
- [ ] Remote MCP support & OAuth over chat channel — proxy MCP auth flows through the user's channel
- [ ] Other channel support (Telegram, Slack, etc.)

## Self-Modification
- [ ] Privileged mode — agent can modify its own codebase, commit to branches, and open PRs via a deploy key (never commits directly to main)

## Automation
- [ ] Task scheduling — cron-like triggers that kick off agent sessions autonomously
- [ ] Schedule management tools — MCP tools the agent can use to create, list, update, and delete its own scheduled tasks

## Operations
- [ ] Deployment — containerise, CI/CD, hosting
- [ ] Monitoring — logging, metrics, alerting, cost tracking
