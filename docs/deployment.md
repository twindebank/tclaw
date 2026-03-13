# Deployment (Fly.io)

## Overview
- Hosted on Fly.io in `lhr` (London), app name: `tclaw`
- Local Docker builds only (`tclaw deploy`) — no auto-deploy on push
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
tclaw deploy logs        # Tail logs
tclaw deploy suspend     # Spin down (scale to 0)
tclaw deploy resume      # Spin up (scale to 1)
```

## First-Time Setup
1. `brew install flyctl && fly auth login`
2. `fly apps create tclaw`
3. `fly volumes create tclaw_data --region lhr --size 1 -a tclaw -y`
4. Set secrets: `tclaw secret set NAME value`, then `tclaw deploy secrets`
5. `tclaw deploy`

## OAuth Callback URL
`https://tclaw.fly.dev/oauth/callback` — set this as the redirect URI in Google OAuth console.

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
