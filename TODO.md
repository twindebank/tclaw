# tclaw Roadmap

## Core Agent
- [x] Agent loop — multi-turn conversations via `--resume` session continuation
- [x] Session management / persistence — session ID captured from CLI and reused across turns
- [x] Tool use — Claude Code executes tools internally; we get results in the stream
- [ ] Streaming responses — send text deltas to the channel as they arrive instead of buffering
- [ ] Stream thinking — surface extended thinking blocks to the channel in real time
- [ ] Stream tool use — show tool calls and results in the channel as they happen

## Memory & Context
- [ ] System and agent memories — markdown files on the filesystem, loaded into context
- [ ] Custom MCPs can be built in — register Go-native MCP servers alongside the agent

## Permissions & Security
- [ ] Tool permissions / 2FA — approve or deny tool calls via the chat channel
- [ ] Multi-tenancy — isolate sessions, memory, and permissions per user/channel

## Connectivity
- [ ] Remote MCP support & OAuth over chat channel — proxy MCP auth flows through the user's channel
- [ ] Other channel support (Telegram, Slack, etc.)

## Automation
- [ ] Task scheduling — cron-like triggers that kick off agent sessions autonomously

## Operations
- [ ] Deployment — containerise, CI/CD, hosting
- [ ] Monitoring — logging, metrics, alerting, cost tracking
