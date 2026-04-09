---
name: deploy-to-prod
description: Guide through Fly.io production deployment
disable-model-invocation: true
allowed-tools: Bash, Read, Edit, Write
---

Guide the user through deploying tclaw to Fly.io for always-on Telegram access. Check each step, auto-generate config where possible, and explain what's happening.

## 1. Prerequisites

Check and report status:
- `fly version` — if missing: `brew install flyctl`
- `fly auth whoami` — if not logged in: tell user to run `! fly auth login`
- `docker info 2>/dev/null` — Docker must be running for local builds

Stop if any prerequisite is missing.

## 2. Prod config section

Check if `tclaw.yaml` has a `prod:` section:
```bash
grep -c "^prod:" tclaw.yaml
```

If missing, **auto-generate it**:

a. Read the existing `local:` section to extract user ID and model:
```bash
grep -A2 "id:" tclaw.yaml | head -1
grep "model:" tclaw.yaml | head -1
```

b. Ask the user for:
- **Fly app name** — check `fly.toml` for existing value first (`grep "^app" fly.toml`)
- **Telegram bot token** — guide them: "Create a bot with @BotFather on Telegram, send /newbot, copy the token"
- **Telegram user ID** — "Send any message to @userinfobot on Telegram to get your ID"

c. Store the bot token in keychain:
```bash
go run . secret set TELEGRAM_BOT_TOKEN <token>
```

d. Append the `prod:` section to `tclaw.yaml` (use the Edit tool):
```yaml
prod:
  base_dir: /data/tclaw
  server:
    addr: 0.0.0.0:9876
    public_url: https://<app-name>.fly.dev
  users:
    - id: <same as local>
      model: <same as local>
      permission_mode: dontAsk
      role: superuser
      channels:
        - type: telegram
          name: mobile
          description: Mobile assistant
          role: assistant
          telegram:
            token: ${secret:TELEGRAM_BOT_TOKEN}
            allowed_users: [<telegram-user-id>]
```

## 3. Fly.io app setup

Check if the Fly app exists:
```bash
fly apps list 2>/dev/null | grep <app-name>
```

If not, guide through creation:
```bash
fly apps create <app-name>
fly volumes create tclaw_data --region lhr --size 1 -a <app-name> -y
```

## 4. Verify fly.toml

Check the `app` name in `fly.toml` matches the Fly app. Update if needed.

## 5. Push secrets

```bash
go run . deploy secrets
```

This scans `tclaw.yaml` for `${secret:*}` references, reads each from the OS keychain, and pushes them to Fly.

## 6. GitHub CI secrets and variables

CI auto-deploys on every push to main. It needs three things set on the GitHub repo:

```bash
# Check what's already set
gh secret list
```

**Secrets** (encrypted, masked in logs):
- `FLY_APP_NAME` — the Fly app name. Stored as a secret so GitHub masks it in deploy logs (prevents leaking the app URL, registry, monitoring dashboard, etc.)
  ```bash
  gh secret set FLY_APP_NAME
  # paste the app name when prompted
  ```

- `FLY_API_TOKEN` — deploy token for Fly.io
  ```bash
  fly tokens create deploy -x 999999h -a <app-name>
  gh secret set FLY_API_TOKEN
  # paste the token when prompted
  ```

- `TCLAW_YAML` — seed config baked into the Docker image (first-boot only, never overwrites live config on the volume). `tclaw config push` updates this automatically, but for first deploy:
  ```bash
  gh secret set TCLAW_YAML < tclaw.yaml
  ```

## 7. Deploy

```bash
go run . deploy
```

This builds a Docker image locally and deploys it to Fly. Subsequent deploys happen automatically via CI on push to main.

## 8. Verify

```bash
fly status -a <app-name>
fly logs -a <app-name> --no-tail 2>/dev/null | tail -20
```

## 9. Credential summary

After deployment, explain the three credential levels:

1. **Config secrets** (just pushed via `deploy secrets`) — Telegram bot token, API keys, provider credentials. These are referenced as `${secret:NAME}` in tclaw.yaml.

2. **Per-user runtime secrets** — credentials the agent collects during chat (GitHub PAT, OAuth tokens). These are auto-managed. To pre-provision them for production:
   ```
   fly secrets set GITHUB_TOKEN_<USER>=ghp_... -a <app-name>
   fly secrets set FLY_TOKEN_<USER>=... -a <app-name>
   ```

3. **Auth token** — the Claude OAuth/API key. If using OAuth locally, the setup token was saved to keychain and pushed with `deploy secrets`. If using an API key, it was also pushed.

Report each step's output clearly. Stop and explain if anything fails.
