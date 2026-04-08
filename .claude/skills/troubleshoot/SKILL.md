---
name: troubleshoot
description: Diagnose why tclaw isn't working — checks config, processes, logs, and prerequisites
disable-model-invocation: true
allowed-tools: Bash, Read, Glob, Grep
---

Diagnose common tclaw issues. Run checks in order, stop at the first problem found, explain what's wrong and how to fix it.

## 1. Prerequisites

```bash
go version        # need 1.26+
node --version    # need 22+
claude --version  # need claude CLI
which tclaw       # installed?
```

Report any missing with install instructions.

## 2. Config

Check `tclaw.yaml` exists and is valid:

```bash
test -f tclaw.yaml && echo "exists" || echo "MISSING"
head -30 tclaw.yaml
```

Common config issues:
- Missing `tclaw.yaml` → run `tclaw init`
- No channels defined → need at least one channel
- Secret references (`${secret:NAME}`) that can't be resolved → run `tclaw secret set NAME value`
- Invalid YAML syntax

## 3. Local server

```bash
# Is the server running?
pgrep -f "tclaw.*serve" || echo "NOT RUNNING"

# Is the port in use?
lsof -i :9876 2>/dev/null || echo "port 9876 free"

# Check for socket file (if socket channel configured)
ls /tmp/tclaw/*/main.sock 2>/dev/null || echo "no socket files"
```

## 4. Authentication

```bash
# Check if Claude OAuth token exists
tclaw secret get CLAUDE_OAUTH_TOKEN 2>/dev/null && echo "OAuth token found" || echo "no OAuth token"

# Check if API key exists
tclaw secret get ANTHROPIC_API_KEY 2>/dev/null && echo "API key found" || echo "no API key"
```

If neither exists, the agent will prompt on first message — but explain this so the user isn't confused.

## 5. Recent logs

If the server is running or was recently running:

```bash
# Check for crash output in terminal
# Check Fly logs if deployed
fly logs --no-tail -a $(grep "^app" fly.toml 2>/dev/null | cut -d'"' -f2) 2>/dev/null | tail -30
```

## 6. Deployment (if applicable)

Only check if `fly.toml` exists:

```bash
test -f fly.toml && echo "fly.toml found" || echo "no fly.toml (local only)"
```

If deployed:
```bash
fly status -a $(grep "^app" fly.toml | cut -d'"' -f2) 2>/dev/null
fly logs --no-tail -a $(grep "^app" fly.toml | cut -d'"' -f2) 2>/dev/null | tail -20
```

Common deployment issues:
- App suspended → `tclaw deploy resume`
- OOM kills → check `NODE_MAX_HEAP_MB` in fly.toml, see docs/deployment.md
- Secret mismatch → `tclaw deploy secrets`
- Stale image → `tclaw deploy` or push to main for CI deploy

## 7. Telegram (if configured)

```bash
grep -A5 "type: telegram" tclaw.yaml 2>/dev/null
```

Common Telegram issues:
- Bot token invalid → recreate with @BotFather
- `allowed_users` not set → messages from other users are silently dropped
- Webhook vs polling confusion → `public_url` in config enables webhooks, otherwise long-polling

## Output format

For each check:
- State what you checked
- Report what you found
- If there's a problem: explain what's wrong and give the exact command to fix it
- If everything looks good: say so and move to the next check

At the end, summarize: either "everything looks healthy" or a numbered list of issues found with fixes.
