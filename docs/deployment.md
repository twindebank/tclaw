# Deployment (Fly.io)

## Overview
- Hosted on Fly.io, app name configured in `fly.toml`
- **GitHub Actions CI deploys automatically on push to main** (`.github/workflows/deploy.yml`) — add `[no-deploy]` to the commit message to skip the deploy (e.g. for doc-only or TODO changes)
- Local deploys also work via `tclaw deploy` (builds locally with Docker)
- Persistent volume `tclaw_data` at `/data` for per-user state
- Health check at `/healthz` on port 9876
- Seed config baked into image at `/etc/tclaw/tclaw.yaml`; copied to persistent volume (`/data/tclaw.yaml`) on first boot. Runtime config lives on the volume so agent mutations survive redeploys.
- Subprocess sandboxing via bubblewrap (mount namespace isolation per user)

## Self-Healing Watchdog

Fly's health check detects an unresponsive machine but **only stops routing to it — it
never restarts it**. A tclaw process that wedges (stops serving `/healthz`: goroutine
starvation, deadlock, fd/memory exhaustion) therefore stays down until a human runs
`fly machine restart`. This bit us once: the machine sat `critical` for hours with
broken outbound DNS while the health check timed out.

`internal/watchdog` closes that gap. When running under Fly (gated on `FLY_MACHINE_ID`),
it probes the process's own `/healthz` on loopback every 30s; after 4 consecutive
failures (~2 min) it logs the wedge (with goroutine count) and `os.Exit(1)`s. Fly's
default `on-fail` restart policy then replaces the process, and tclaw recovers from
persisted queue/outbox state — turning a multi-hour outage into a ~10s blip.

- **Scope is self-liveness only** — it restarts when the process can't answer its own
  health endpoint (what Fly measures), not when an external dependency (Telegram, Gmail)
  is down. Restarting on external-dependency failure risks a crash loop if that
  dependency is globally down, so it is deliberately out of scope.
- A 90s boot grace period and the 4-failure threshold keep a transient blip from
  triggering a needless restart.

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

## Credentials

Three categories, distinguished by who creates the key and whether config can name it:

**Boot secrets** — `${boot:NAME}` in `tclaw.yaml`, resolved from the OS keychain locally and Fly
secrets in prod as config loads, then scrubbed from the environment. Set with `tclaw secret set NAME`
and pushed with `tclaw deploy secrets`. Keep this set small: almost nothing must exist at boot.

**Credential slots** — declared in config, seeded into the encrypted store at
`cred/<type>/<label>/<field>`, and the only credentials the rest of the config can reference by name:

```yaml
credential_slots:
  - type: git                      # a tool package name, or "git" for repo/dev/vault access
    label: default
    description: GitHub PAT — used by any repo that names no other slot
    fields:
      token: ${boot:GITHUB_TOKEN}

  - type: git
    label: homeassistant
    description: Scoped PAT, Home Assistant config repo only
    # no fields — declared but unset; fill it from a phone with a secret form
```

A slot with no `fields:` is created empty. That is the point of declaring one: it can be referenced
now and filled later via `secret_form_request` with a `credential` target, without a config edit or a
deploy. `credential_list` shows every slot, whether each field is set, and the exact form target that
fills it; `credential_clear` drops a value while leaving the slot declared.

**Runtime credentials** — OAuth tokens, per-channel bot tokens, remote MCP headers. Keyed by the
system, never referenceable from config.

The agent can name a credential and trigger its collection, but never read one. Because agent-facing
keys forbid slashes, nothing it names can reach the `cred/` namespace.

## Repos

Repos are declared per-user, cloned on boot, and reached through a per-user git proxy that injects
credentials server-side — no clone holds a token, and the agent uses ordinary git:

```yaml
repos:
  - name: homeassistant-config
    repo: owner/homeassistant-config
    branch: main
    description: Live Home Assistant config
    access: pull_requests_only     # read_only | pull_requests_only | full_write
    credential: homeassistant      # a credential_slots label with type git; omit for the default
    visible_to_channels: [homeassistant]
    fetch_every: 6h
    drop_to_read_only_at: 2026-12-01T00:00:00Z
    drop_clone_if_unused_for: 2160h
```

**Access tiers** are enforced by the proxy, not the tools — the agent runs git itself, so anything
checked tool-side could be bypassed with raw git:

- `read_only` — fetch only; the clone is mounted read-only and `repo_sync` resets it to the remote.
- `pull_requests_only` — push any branch except the default one, and open PRs. Writes to the default
  branch, tags and other non-branch refs are refused.
- `full_write` — push anywhere.

The agent raises a tier with `repo_request_access`, which prompts you in the chat and applies only on
your reply; it cannot answer its own prompt. Lowering applies immediately. `drop_to_read_only_at`
withdraws push access at that instant — the repo and clone stay, only the tier drops, and the channel
is told. `drop_clone_if_unused_for` is disk hygiene: the clone goes, the entry stays, and the next
sync recreates it.

**Channel scoping** (`visible_to_channels`) is enforced twice: the per-turn `--add-dir` list carries
only that channel's repos and bwrap masks the rest behind an empty tmpfs, and the repo tools report
scoped-out repos as not found. An unknown channel fails closed.

`config_set` cannot change `repos`, `credential_slots`, `users`, `tool_groups`, `allowed_tools`,
`disallowed_tools` or `creatable_groups` — those are yours to set with `tclaw config push`. Without
that, the agent could grant itself access directly and the confirmation prompt would be decorative.

## Rulebooks

Standing decisions — how a task is done on a given channel — live in `<user>/memory/rules/`, one file
per area, shared across channels. Nothing needs configuring: the directory is created on first use and
the `channel-rules` skill is seeded into every user's `home/.claude/skills/`.

Each channel's `memory/channels/<name>/CLAUDE.md` decides what loads there:

- `@../../rules/<file>.md` pulls a rulebook into context on every turn of that channel.
- Anything else is listed by name, and read on demand.

Every rulebook is readable from every channel either way; the lists only decide what arrives without
being asked for. `rule_list` shows what exists and where each one is referenced.

**Changing one needs you.** The agent calls `rule_propose` with the complete proposed text, you get the
text in the chat, and it is written on your "yes" — by tclaw, outside the sandbox. Direct writes to the
rules directory are refused by the `rules-gate` hook, so there is no other route. Editing which
rulebooks a channel loads is ordinary agent memory work and needs no approval.

Hooks are registered in each user's `settings.json` at boot from `hooks.Manifest`, pointing at whatever
`tclaw-hooks` resolves to on `PATH` — `/usr/local/bin/tclaw-hooks` in the image, which the Dockerfile
fails the build without, because a registration pointing at nothing would fail on every tool call.

**Running locally**, `tclaw build` puts it in `bin/` and `tclaw install` puts it on `PATH`. Until it is
on `PATH`, boot logs a warning and the agent runs unenforced: it can write rulebooks directly, and
nothing else looks different. The tools and the channel indexes work either way.

## Personal Knowledge Base

The vault is an ordinary declared repo. `knowledge:` only says which one it is, where it mounts, and
the git identity for the commits the agent makes:

```yaml
repos:
  - name: knowledge
    repo: owner/knowledge-base
    access: full_write

knowledge:
  repo: knowledge                  # names the repos entry above
  mount_at: knowledge              # dir under <user>/; keeps ../knowledge valid
  commit_name: My Name
  commit_email: me@users.noreply.github.com
  claude_dirs:                     # optional; <name under home/.claude/>: <path in the vault>
    skills: claude/skills
    agents: claude/agents
```

Validation rejects a `knowledge.repo` that names no declared repo, or one whose tier cannot push —
the agent commits and pushes the vault every turn, so a read-only vault would silently fail to save.
Guidance (vault conventions, git workflow) is seeded as a `knowledge` skill in the user's
`home/.claude/skills/`; the agent loads it on demand, and tclaw auto-syncs after every turn.

### Installing what the vault carries

`claude_dirs` maps a directory in the agent's Claude config directory to the vault directory that
fills it. On every boot the contents are copied across, so a skill, a subagent or a rule file written
on a laptop is available here too, without a deploy. The CLI discovers `skills/` and `agents/` on its
own; anything else is there for a skill to read by path.

Both halves are checked at config load. The vault path must be a plain relative path — absolute, or
climbing out with `..`, is rejected, because it is joined onto the clone and one that escaped would
install files from anywhere on the volume. The name must be a single directory with no separator in
it, because it is joined onto `home/.claude/`.

They are copied **before** tclaw's own skills, so `knowledge`, `channel-rules` and `gws-tclaw` always
win a name clash. The vault clone is writable by the agent, and a file it can write must not be able
to replace the skill that explains tclaw's own machinery. Nothing else here is a new capability: the
agent can already write those directories directly. What this adds is the vault as the place they are
kept.

## Turn Limits

`max_turns` caps how many agentic turns the claude CLI takes inside one message. When the cap is hit
the turn ends and the chat gets `⚠️ Turn limit reached (N/N). Send another message to continue.`

Set it per user, per channel, or both:

```yaml
users:
  - id: alice
    max_turns: 50            # applies to every channel that sets none of its own
    channels:
      - name: email
        max_turns: 10        # answer and stop
      - name: dev
        max_turns: 150       # long multi-step work
      - name: phone          # no max_turns — inherits the 50 above
```

The channel value wins where it is set. With neither set, the built-in default is 10
(`defaultMaxTurns` in `internal/agent/agent.go`). Zero means inherit, so it cannot be used to stop a
channel dead; a negative value is rejected at config load.

The agent sets this itself when it stands up a channel: `channel_create` and `channel_edit` both take
`max_turns`, and `channel_read` shows the current value. Passing `0` to `channel_edit` clears the
channel's own limit and puts it back on the user-level one.

## The Retro Queue

Corrections are captured as they happen and judged much later, by a session that did not make the
mistake. The `lesson-capture` hook runs on every message you send, matches it against a set of
pushback patterns, and appends the matching ones verbatim to
`home/.claude/feedback/inbox.jsonl`. A hook that refuses a tool call files its refusal there too —
being stopped is evidence a rule did not hold on its own.

Nothing is judged in the moment. A model grading its own work in the same conversation agrees with
itself, so the queue is read later, and the only thing the hook puts into the agent's context is what
the `!log` marker means.

- **`!log` anywhere in a message files it** whether or not it reads like a correction, and means: do
  not action or debate this now. A message that only writes *about* the marker, quoted in backticks
  or a fenced block, is not filed.
- **A prompt over 2000 characters is a paste**, not something typed at you, and is skipped.
- **A task brief carrying a ground rule is not a correction.** "Add a retry, and don't touch the
  tests" is work being assigned; "don't use a mock there" on its own is an objection.
- **Once three rows are waiting**, the hook tells the agent so, and says so again each time the queue
  grows by another three. The count is in the message, so a repeat reads as a repeat.

`CLAUDE_CONFIG_DIR` is set on the agent subprocess to the directory the CLI already uses. Hooks run
under a shell that reads no profile, so left unset a skill's `$CLAUDE_CONFIG_DIR/feedback` path
resolves to the filesystem root, which the sandbox refuses.

Draining the queue is a skill, not a tclaw feature: put one in the vault and name its directory in
`knowledge.claude_dirs`.

## Message Debounce

A burst of user messages that lands close together — most visibly a photo album, which every
channel delivers as **separate** messages — would otherwise start one agent turn per message
(each seeing a single attachment). The `message_debounce` per-user knob coalesces same-channel
user messages that arrive within a rolling window into a **single** turn:

```yaml
users:
  - id: alice
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
- `tclaw deploy secrets` scans the **prod** environment of `tclaw.yaml` for `${boot:NAME}` refs, reads each from the keychain, and pushes them to Fly in one call. Only prod is scanned: the environments reuse names for different values (the local dev bot and the production bot are both a Telegram token), so scanning the whole file would push a dev credential to production.
- A ref that is missing locally but **already set on Fly** is left alone, not treated as an error — it was pushed from another machine and is unchanged. Only a secret missing from both is fatal. Without this, the command would be unusable from any machine not holding the full prod set.
- Every value is resolved before anything is sent, so a missing secret aborts with nothing pushed. `tclaw config push` syncs secrets **first** for the same reason: it performs three writes (volume config, Fly secrets, seed secret) and must not leave the volume updated with the rest skipped.
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
tclaw render-pdf a.md b.pdf   # Render a markdown file to PDF with the same engine the agent uses
```

## Documents

`document_send_pdf` renders a markdown file from the user's memory directory into a PDF and sends it to
the chat. It is in the `channel_messaging` tool group, so any channel that already sends messages has it.

Delivery needs a transport that implements `channel.FileSender`. Telegram does; socket and stdio do not,
and the tool says which transport refused rather than failing quietly.

A document can carry a credential the agent cannot read: `${cred:wifi_password}` in the markdown resolves
the store key `doc_wifi_password` as the PDF is built. Only `doc_`-prefixed keys are reachable, so nothing
tclaw provisions for itself can be printed into a document. Set one with `secret_form_request` using the
full key. On a laptop the equivalent is a 1Password reference filled in by `op inject`; tclaw has no
1Password access, so the value has to be put into its own store once.

The renderer (`internal/libraries/markdownpdf`) is pure Go and deliberately not a browser: the image has
no chromium and no python, and the VM has little spare memory once the CLI's V8 heap is accounted for.
`tclaw render-pdf` puts the same engine behind a CLI command, so a document rendered on a laptop and one
rendered in production lay out identically. The CLI cannot fill credentials — it has no access to the
store — so it warns and prints any placeholder literally. A document carrying one has to go through the
tool.

Text is limited to what the PDF core fonts cover (Windows-1252). Ordinary punctuation is fine — em
dashes, en dashes, degree signs, accented letters. Emoji are not, and a render fails naming the character
instead of dropping it.

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

**Moving a tool package to a different group is a two-step deploy, and the order matters.** A package's
group comes from the binary, and which groups a channel holds comes from the config, so the two have to
cross over without leaving a gap. Push the config **first**, granting the new group while leaving the old
one in place:

- old binary + new config — the new group is unknown, resolves to no tools, and the old group still
  grants them
- new binary + new config — the tools come from the new group, which the channel now has

Deploying first inverts that: the moment the new binary boots, the tools leave the old group and no
channel has been granted the new one, so every affected channel loses them until the config lands.

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
