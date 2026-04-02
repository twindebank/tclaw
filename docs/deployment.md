# Deployment (Fly.io)

## Overview
- Hosted on Fly.io, app name configured in `fly.toml`
- **GitHub Actions CI deploys automatically on push to main** (`.github/workflows/deploy.yml`) — add `[no-deploy]` to the commit message to skip the deploy (e.g. for doc-only or TODO changes)
- Local deploys also work via `tclaw deploy` (builds locally with Docker)
- Persistent volume `tclaw_data` at `/data` for per-user state
- Health check at `/healthz` on port 9876
- Seed config baked into image at `/etc/tclaw/tclaw.yaml`; copied to persistent volume (`/data/tclaw.yaml`) on first boot. Runtime config lives on the volume so agent mutations survive redeploys.
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
tclaw deploy fly-config  # Push local fly.toml to Fly (no rebuild)
tclaw deploy suspend     # Spin down (scale to 0)
tclaw deploy resume      # Spin up (scale to 1)
tclaw config push        # Push local config to remote Fly volume
tclaw config pull        # Pull remote config to local
tclaw config diff        # Show differences between local and remote config
```

## Config Lifecycle

The runtime config lives on the persistent Fly volume at `/data/tclaw.yaml`. On first boot (or after a volume wipe), the seed config baked into the image at `/etc/tclaw/tclaw.yaml` is automatically copied to the volume. All agent mutations (channel create/edit/delete) write to the volume copy, so they survive redeploys.

The image-baked seed config comes from the `TCLAW_YAML` GitHub secret, written to `tclaw.yaml` during CI and COPYed into the image at `/etc/tclaw/tclaw.yaml`. This seed is only used on first boot (or after a volume wipe) — it never overwrites the live config on the persistent volume.

**Commands:**

- `tclaw config push` — overwrites the remote volume config with your local `tclaw.yaml`, syncs secrets, and updates the `TCLAW_YAML` seed secret.
- `tclaw config pull` — pulls the remote volume config to your local `tclaw.yaml`. Use this to get agent-created changes back locally.
- `tclaw config diff` — shows a unified diff between local and remote configs.

**Typical workflow:**

```
tclaw config diff          # Preview what's different
tclaw config push          # Push local config to remote volume + sync secrets + update seed
tclaw config pull          # Pull agent changes back to local
```

## Fly Platform Config (fly.toml)

`fly.toml` controls Fly platform settings: concurrency limits, health checks, VM size, environment variables. It's gitignored because it contains the app name.

**CI deploys don't update fly.toml settings** — they only deploy new code. To change platform config, use:

```
tclaw deploy fly-config    # Diffs local fly.toml against live, then redeploys the current image
```

This redeploys the same Docker image with the updated `fly.toml` — no rebuild, no code changes. Use it for:
- Changing concurrency limits (`hard_limit`, `soft_limit`, `type`)
- Adjusting health check intervals/timeouts
- Changing VM size or memory
- Updating environment variables

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
