# Deployment (Fly.io)

## Overview
- Hosted on Fly.io, app name configured in `fly.toml`
- **GitHub Actions CI deploys automatically on push to main** (`.github/workflows/deploy.yml`) — add `[no-deploy]` to the commit message to skip the deploy (e.g. for doc-only or TODO changes)
- Local deploys also work via `tclaw deploy` (builds locally with Docker)
- Persistent volume `tclaw_data` at `/data` for per-user state
- Health check at `/healthz` on port 9876
- Config baked into image at `/etc/tclaw/tclaw.yaml` (unified multi-env file, `--env prod` selects the prod section)
- Subprocess sandboxing via bubblewrap (mount namespace isolation per user)

## Secret Management
- Secrets stored locally in OS keychain via `tclaw secret set NAME value`
- `tclaw deploy secrets` scans `tclaw.yaml` for `${secret:NAME}` refs across all environments, reads each from keychain, pushes to Fly in one call
- At runtime: Fly injects secrets as env vars → config resolves them → `main.go` scrubs env vars before spawning Claude subprocesses
- Per-user tool secrets (GitHub PAT, Fly API token) are deployed as `<PREFIX>_<USER>` Fly secrets and seeded into the encrypted store on boot (see architecture docs for the seeding pattern)

## Commands
```
tclaw deploy             # Build locally + deploy to Fly
tclaw deploy secrets     # Push keychain secrets to Fly
tclaw deploy status      # Check app status
tclaw deploy logs        # Show recent logs (same as tclaw logs)
tclaw deploy suspend     # Spin down (scale to 0)
tclaw deploy resume      # Spin up (scale to 1)
tclaw config sync        # Sync tclaw.yaml between local and remote
tclaw config diff        # Show differences between local and remote config
```

## Config Sync

`tclaw config sync` keeps the local `tclaw.yaml` and the remote copy on the Fly volume in sync. It handles the common case where the agent creates channels at runtime in production (written to the remote config) while you edit the config locally.

**What it does:**

1. Reads both local `tclaw.yaml` and the remote config via `fly ssh console`
2. Parses both configs and compares channels per environment/user
3. Detects expired ephemeral channels on the remote and skips them (based on `created_at` + `ephemeral_idle_timeout`, default 24h)
4. Flags remote-only channels for manual review (printed to stdout with a note to use `tclaw config diff`)
5. Pushes the merged config to both local disk and the remote Fly volume
6. Runs `tclaw deploy secrets` automatically to sync secrets alongside the config

**What it does not do (yet):**

- Auto-merge remote-only channels into the local config. Full YAML merge is complex, so remote-only channels are flagged for review. Use `tclaw config diff` to inspect them, then edit `tclaw.yaml` manually if you want to keep them.

**Typical workflow:**

```
tclaw config diff        # Preview what's different
tclaw config sync        # Sync configs + secrets
```

`tclaw config diff` reads both configs via SSH and runs a unified diff (`diff -u`) with `remote:` and `local:` labels. If the configs are identical it prints a confirmation and exits.

## First-Time Setup
1. `brew install flyctl && fly auth login`
2. `fly apps create your-app-name`
3. `fly volumes create tclaw_data --region lhr --size 1 -a your-app-name -y`
4. Set secrets: `tclaw secret set NAME value`, then `tclaw deploy secrets`
5. `tclaw deploy`

## OAuth Callback URL
`https://your-app.fly.dev/oauth/callback` — set this as the redirect URI in your OAuth provider console (e.g. Google Cloud Console).

## Memory Tuning

The Fly VM runs at 256MB (free tier). tclaw itself uses ~15MB; the claude CLI (Node.js) is the main consumer. To prevent the CLI from eating all available memory and getting OOM-killed silently:

- **`NODE_MAX_HEAP_MB`** in `fly.toml` `[env]` caps the V8 heap via `NODE_OPTIONS=--max-old-space-size=<value>`. Currently set to `128`.
- When the heap limit is hit, Node.js exits with a JS heap OOM error instead of the kernel OOM-killer firing. The agent catches this and notifies the user on the channel.
- **To increase:** raise `NODE_MAX_HEAP_MB` in `fly.toml` and redeploy. If you also raise the VM memory (`[[vm]] memory`), you have more headroom — budget ~80MB for kernel + tclaw + system, the rest for the CLI.
- **To disable:** remove `NODE_MAX_HEAP_MB` from `fly.toml`. The CLI will use whatever memory is available (and risk OOM-kill with no user notification).

| VM memory | NODE_MAX_HEAP_MB | Notes |
|-----------|-----------------|-------|
| 256mb     | 128             | Too tight — OOM-kills on fresh sessions |
| 512mb     | 350             | Current prod. Handles fresh sessions and heavy turns |
| 1024mb    | 800             | No practical constraints |

## CI (optional)
GitHub Actions workflow at `.github/workflows/deploy.yml` — manual trigger only (`workflow_dispatch`). Needs `FLY_API_TOKEN` GitHub secret from `fly tokens create deploy -x 999999h`.
