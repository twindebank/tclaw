---
name: quickstart
description: Guide a new user through local tclaw setup
disable-model-invocation: true
allowed-tools: Bash
---

Walk through local tclaw setup step by step. Be concise — print what you find, suggest fixes, move on.

1. **Check prerequisites:**
   - `go version` (need 1.26+)
   - `node --version` (need 22+)
   - `claude --version` (need claude CLI installed)
   Report any missing with install instructions. Stop here if anything critical is missing.

2. **Check config:**
   - If `tclaw.yaml` exists, confirm it looks valid (`head -20 tclaw.yaml`)
   - If not, run `go run . init` — the user will complete the interactive prompts

3. **Check installation:**
   - `which tclaw` — if not found, suggest `make install-dev && source ~/.zshrc`
   - Or they can use `go run .` directly from the repo

4. **Explain the workflow:**
   - Terminal 1: `tclaw serve` (starts the agent server)
   - Terminal 2: `tclaw chat` (connects the TUI chat client)
   - On first message, the agent walks through authentication if needed

5. **Mention next steps:**
   - Type messages to chat with the agent
   - Type `reset` for reset options, `stop` to cancel a turn, `compact` to compress context
   - For mobile access via Telegram, ask the agent "set up a Telegram channel for me"
   - For production deployment, use the `/deploy-to-prod` skill

Do NOT start the server or chat client — just guide the user through setup.
