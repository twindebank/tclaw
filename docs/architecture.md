# tclaw Architecture

tclaw spawns isolated `claude` CLI subprocesses — one per user — and manages communication through multiple transport channels. It does **not** use the Claude Agent SDK; it drives the CLI binary directly via `--output-format stream-json`.

```
  Channels (socket, Telegram, stdio)
            │
       ┌────▼─────┐
       │  Router   │  per-user lifecycle, lazy start/stop
       └────┬──────┘
            │
  ┌─────────▼──────────┐
  │  claude CLI process │  spawned per turn, stream-json output
  └─────────┬──────────┘
            │
  ┌─────────▼──────────┐
  │  MCP Server        │  per-user, localhost:<random>, bearer token
  └────────────────────┘
```

All packages live under `internal/`. See each package's doc comment for its responsibility.

## Dependency Layers

Dependencies flow strictly downward — no circular imports.

```
Layer 1:  Pure types (user, claudecli, store.Store, secret.Store)
Layer 2:  Domain models (credential, schedule, channel.Channel)
Layer 3:  Managers (credential.Manager, schedule.Store, channel.RuntimeStateStore)
Layer 4:  Stateless handlers (oauth, mcp.Handler, mcp/discovery)
Layer 5:  Channel implementations (socketchannel, stdiochannel, telegramchannel)
Layer 6:  Agent loop (agent.Run — spawns CLI, handles auth, manages turns)
Layer 7:  HTTP server (oauth.CallbackServer — callbacks, webhooks, health)
Layer 8:  Tool implementations (channeltools, credentialtools, google, etc.)
Layer 9:  Configuration (config — YAML parsing, secret resolution)
Layer 10: CLI dispatch (cli/ — subcommand routing)
Layer 11: Orchestration (router, main)
```

## Security Model

Four boundaries protect user data and the host system:

### 1. Subprocess Isolation
- **Environment allowlist** — only safe env vars (PATH, TERM, LANG, etc.) reach the subprocess. Cloud credentials, SSH agents, GitHub tokens, and tclaw internals are excluded. See `agent/handle.go:allowedEnvPrefixes`.
- **Filesystem sandbox** (Linux only) — bubblewrap mount namespace isolates each user's subprocess. Only their own memory/home dirs are writable; system paths are read-only; other users' data is invisible. See `agent/sandbox.go`.

### 2. Channel Boundary
- Socket and stdio channels are blocked in non-local environments (no authentication).
- Telegram restricts access via user-level `telegram.user_id` — messages from other users are dropped.

### 3. MCP Tool Boundary
- Per-user MCP server on localhost with random bearer token.
- 1 MiB request body limit, audit logging, permission-gated via `tool_groups`.

### 4. Secret Boundary

Secrets fall into three categories, distinguished by who creates the key and whether config can
name it:

- **Boot secrets** (`${boot:NAME}`) — operator-provisioned, resolved from the keychain or Fly as
  config loads, then scrubbed from the environment. Never enter the store.
- **Credential slots** (`credential_slots:`) — declared, seeded into `cred/<type>/<label>/<field>`,
  and the only credentials the rest of the config can reference by name. A slot may be declared
  without a value and filled later from a secret form, which is what lets a credential be set up
  without a deploy.
- **Runtime credentials** — OAuth tokens, per-channel transport tokens, remote MCP headers. Keyed by
  the system and deliberately unreferenceable, so a repo can never point its auth at one.

Two invariants hold across all three: **the agent may name a secret but never read one** (no tool
returns a value; consumers read server-side and proxies inject at the network boundary), and
**agent-facing keys are validated against `^[a-z0-9_]+$`**, which cannot express a slash — so nothing
the agent names can reach the `cred/` or `channel/` namespaces.
- NaCl secretbox encryption with per-user HKDF-derived keys. See `libraries/secret/`.
- Boot secrets via `${boot:NAME}` (keychain → env var fallback, then scrubbed).
- Runtime secrets via encrypted store (agent-collected OAuth tokens, API keys).
- Fly secrets seeded into encrypted store on boot, then scrubbed from env.
- **Git auth and repo access** — every repo clone, the knowledge vault included, points its origin at a
  per-user localhost proxy (`internal/gitproxy`) rather than github.com. The proxy resolves the repo by
  its tracked name, injects `Authorization` server-side from the repo's credential slot, and forwards
  to a **fixed** github.com upstream — so a credential cannot reach another host whatever URL a repo
  claims. Git's credential helper runs inside the sandbox, so injecting auth at the network boundary is
  the only way to grant push capability without disclosure.

  The proxy is also where **access tiers** are enforced, because the agent runs git itself and anything
  checked tool-side is bypassable with raw git. `read_only` refuses the push advertisement;
  `pull_requests_only` parses the pkt-line ref commands and refuses any write to the default branch,
  plus tags and other non-branch refs; `full_write` passes through. A body that cannot be parsed — or
  carries an encoding we do not decode — refuses the push rather than guessing at its effects.

  Raising a repo's tier needs the user: `repo_request_access` arms a `PendingAction` and sends the
  prompt straight to the chat, and only a genuine user reply confirms it (`internal/router/done.go`).
  Confirmations expire, so a late "yes" cannot act on a forgotten prompt.
- **Remote MCP auth** — the same pattern generalizes to every connected remote MCP server
  (`internal/remotemcpproxy`). The `--mcp-config` (bind-mounted read-only into the sandbox) points each
  remote at a token-free `http://127.0.0.1:<port>/<name>` URL carrying only a benign proxy-hop token;
  the per-user proxy resolves the real upstream URL and credentials from the encrypted store, injects
  the `Authorization` / static headers server-side (refreshing an expired OAuth token on demand), and
  pins to registered server names. So no third-party MCP token — including a remote browser's API key —
  ever lands in a sandbox-readable file. The proxy also retries connection-level failures (reset / EOF /
  refused) so an autostop upstream that resets the connection while it cold-starts from sleep is waited
  out rather than surfaced to the agent as an immediate 502. Registration (`discovery`) shares the same
  retry, so adding a sleeping server doesn't fail on the request that wakes it.
- **Remote MCP OAuth quirks** — the discovery chain follows RFC 8414 literally, including inserting
  `/.well-known/oauth-authorization-server` **between host and path** for an issuer that has one
  (`https://www.strava.com/mcp-issuer`); appending to the origin instead 404s and breaks discovery
  before it starts. Registration sends `client_name`, because some issuers implement "dynamic"
  registration as an allowlist of recognised clients over one pre-provisioned app and reject anything
  else as `invalid_client_metadata`. Scopes come from the **resource** metadata's `scopes_supported`,
  not the auth server's — the resource is what refuses an under-scoped token.
- **Loopback OAuth (manual paste)** — an authorization server may accept any `redirect_uri` at
  registration and only enforce its real allowlist at the authorization endpoint. Strava allows
  loopback addresses only, so tclaw's hosted `https://<app>/oauth/callback` is refused outright and no
  callback can ever arrive. `remote_mcp_add` therefore probes the authorization endpoint before
  committing the user to a flow (`discovery.RedirectURIAccepted`); on a 4xx it retries with a loopback
  redirect and returns `pending_manual_auth` instead. The user approves in their browser, lands on a
  page that fails to load, and pastes that URL back — `remote_mcp_auth_complete` verifies the state,
  redeems the code with the stored PKCE verifier, and finishes the registration. The verifier lives in
  the encrypted store (`RemoteMCPAuth.PendingExchange`, 30-minute TTL) because the paste arrives on a
  later turn in a different subprocess. A probe that cannot reach the server keeps the hosted callback:
  downgrading a working server to a manual paste on a transient network error would be worse than the
  error. Refresh is unaffected — it never involves a redirect URI.
- **Cold-start budget** — the retry spans ~40s (`discovery.DefaultColdStartRetry`), sized for an upstream
  that boots a browser before it serves rather than a typical web service. The agent subprocess gets a
  matching `MCP_TIMEOUT`: the claude CLI drops an MCP server whose handshake times out, and it drops it
  for the whole turn with no error the agent can report — so a budget shorter than the upstream's cold
  start costs the agent that server's entire tool surface, silently.
