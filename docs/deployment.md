# Deployment (Fly.io)

## Overview
- Hosted on Fly.io, app name configured in `fly.toml`
- **GitHub Actions CI deploys automatically on push to main** (`.github/workflows/deploy.yml`) — add `[no-deploy]` to the commit message to skip the deploy (e.g. for doc-only or TODO changes)
- Local deploys also work via `tclaw deploy` (builds locally with Docker)
- Persistent volume `tclaw_data` at `/data` for per-user state
- Health check at `/healthz` on port 9876
- Seed config baked into image at `/etc/tclaw/tclaw.yaml`; copied to persistent volume (`/data/tclaw.yaml`) on first boot. Runtime config lives on the volume so agent mutations survive redeploys.
- Subprocess sandboxing via bubblewrap (mount namespace isolation per user)

## Google Workspace Skills

The agent learns Google Workspace (`gws`) command syntax from **skills**, not a hand-maintained tool
description. The Dockerfile runs `gws generate-skills` at build time (kept in lockstep with the
installed gws version) and bakes the ~95 `SKILL.md` files into the image at `/etc/tclaw/gws-skills/`.
On each boot the router seeds them into every user's `home/.claude/skills/` (`seedGWSSkills`), where the
claude CLI auto-discovers them — same pattern as the knowledge skill.

A tclaw-authored `gws-tclaw` skill (embedded, seeded last so it wins) explains the one thing the
generated skills get wrong for tclaw: the agent invokes gws through the `google_workspace` **MCP tool**
(`command`/`params`/`json`), not the shell, because the OAuth token is injected server-side. It also
carries tclaw-discovered API gotchas (calendar patch→400, sheets checkboxes/hyperlinks, PDF extraction)
that the generated skills don't cover. To add a new gotcha, edit `internal/router/gws_tclaw_skill.md` —
do **not** grow the `google_workspace` tool description.

In local (non-container) dev the baked dir is absent, so seeding is skipped silently; the agent falls
back to the trimmed tool description and `google_workspace_schema`.

## Personal Knowledge Base

A user can mount a git-backed markdown vault as the agent's durable knowledge store. The agent reads
it on demand and writes new knowledge back with raw git (pull / commit / push). Enable it per-user in
`tclaw.yaml`:

```yaml
users:
  - id: theo
    # Let the agent run git against the vault. Scoped so only git is auto-approved.
    allowed_tools: ["Bash(git *)", "Read", "Edit", "Write"]
    knowledge:
      repo: owner/knowledge-base   # owner/repo shorthand or full HTTPS URL
      branch: main                            # optional, defaults to main
      commit_name: My Name                    # optional git identity for agent commits
      commit_email: me@users.noreply.github.com
```

How it works:
- On boot, tclaw clones the vault to `<user>/knowledge/` and mounts it `--add-dir` (writable in the sandbox).
- Auth reuses the existing `github_token` secret. The PAT **must have write scope** on the vault
  (repotools only needs read). Pushes are authenticated by a per-user localhost git-auth proxy, so the
  token never enters the agent subprocess — see architecture docs (Security Model).
- Guidance (vault conventions, git workflow) is seeded as a `knowledge` skill in the user's
  `home/.claude/skills/`; the agent loads it on demand.

## Message Debounce

A burst of user messages that lands close together — most visibly a photo album, which every
channel delivers as **separate** messages — would otherwise start one agent turn per message
(each seeing a single attachment). The `message_debounce` per-user knob coalesces same-channel
user messages that arrive within a rolling window into a **single** turn:

```yaml
users:
  - id: theo
    message_debounce: "1s"   # optional; unset defaults to 1s, "0s" disables
```

- **Default 1s** when unset — every user message is held ~1s so siblings can land, then all
  queued same-channel user messages are joined into one turn (accepting the small latency on
  lone messages). Set `"0s"` to opt out and process every message immediately.
- The window **resets on each arrival** (a trickling album stays together), bounded by an
  internal 5s cap so a steady stream can't defer processing forever.
- **Control commands** (`stop`, `login`, `auth`, `compact`, and the fresh-session synonyms
  `new`/`reset`/`clear`/`delete`) are never batched — they always run on their own turn.
- Implemented once at the queue layer (`internal/queue/queue.go`), so it covers every channel
  and plain-text bursts too, not just Telegram albums.

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

## CI

GitHub Actions deploys automatically on every push to main (`.github/workflows/deploy.yml`). Add `[no-deploy]` to the commit message to skip.

Required GitHub configuration:

| Type | Name | How to set |
|------|------|-----------|
| Secret | `FLY_APP_NAME` | `gh secret set FLY_APP_NAME` — stored as a secret so GitHub masks the app name, URL, and registry in deploy logs |
| Secret | `FLY_API_TOKEN` | `fly tokens create deploy -x 999999h`, then `gh secret set FLY_API_TOKEN` |
| Secret | `TCLAW_YAML` | `gh secret set TCLAW_YAML < tclaw.yaml` (seed config for first boot only) |
