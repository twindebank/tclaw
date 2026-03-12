# Deployment (Fly.io)

## Overview
- Hosted on Fly.io in `lhr` (London), app name: `tclaw`
- Local Docker builds only (`tclaw deploy`) — no auto-deploy on push
- Persistent volume `tclaw_data` at `/data` for per-user state
- Health check at `/healthz` on port 9876
- Config baked into image at `/etc/tclaw/tclaw.yaml` from `tclaw.deploy.yaml`

## Secret Management
- Secrets stored locally in OS keychain via `tclaw secret set NAME value`
- `tclaw deploy secrets` scans `tclaw.deploy.yaml` for `${secret:NAME}` refs, reads each from keychain, pushes to Fly in one call
- At runtime: Fly injects secrets as env vars → config resolves them → `main.go` scrubs env vars before spawning Claude subprocesses

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
3. `fly volumes create tclaw_data --region lhr --size 1 -a ${{ vars.FLY_APP_NAME }} -y`
4. Set secrets: `tclaw secret set NAME value`, then `tclaw deploy secrets`
5. `tclaw deploy`

## OAuth Callback URL
`https://your-app.fly.dev/oauth/callback` — set this as the redirect URI in Google OAuth console.

## CI (optional)
GitHub Actions workflow at `.github/workflows/deploy.yml` — manual trigger only (`workflow_dispatch`). Needs `FLY_API_TOKEN` GitHub secret from `fly tokens create deploy -x 999999h`.
