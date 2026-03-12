# tclaw

Multi-user Claude Code host. Spawns isolated `claude` CLI subprocesses with persistent memory, multi-channel communication, OAuth connections, scheduling, and MCP tool extensibility.

## Quick Start

```bash
# 1. Install tclaw (pick one)
make install-dev   # Development — runs from source, code changes reflected immediately
make install       # Production — compiled binaries in $GOPATH/bin

# 2. After install-dev, activate the shell function
source ~/.zshrc    # or open a new terminal

# 3. Set your API key
tclaw secret set ANTHROPIC_API_KEY sk-ant-...

# 4. Copy and edit the example config
cp tclaw.example.yaml tclaw.yaml

# 5. Start the agent server
tclaw serve

# 6. Connect the chat client (in another terminal)
tclaw chat
```

## Installation

### Development (recommended for local work)

```bash
make install-dev
source ~/.zshrc   # or open a new terminal
```

Adds a shell function to `~/.zshrc` that runs `go run .` from the repo. Code changes are reflected immediately — no rebuild needed.

### Compiled binaries

```bash
make install
```

Installs `tclaw` and `tclaw-chat` to `$GOPATH/bin`. Faster startup but requires re-running after code changes.

### Uninstall

```bash
make uninstall   # removes both binaries and shell functions (safe if either is missing)
```

## Documentation

- **[Features](docs/features.md)** — what tclaw can do: channels, memory, auth, connections, scheduling, MCP tools, etc.
- **[Architecture](docs/architecture.md)** — package map, dependency layers, data flows, auth flows, directory layout, secret management, environment configuration.
- **[Deployment](docs/deployment.md)** — Fly.io deployment, secrets, commands, first-time setup.

## Commands

| Command | Description |
|---------|-------------|
| `tclaw serve` | Start the agent server |
| `tclaw serve --dev` | Hot-reload server (restarts on `.go` changes, requires `air`) |
| `tclaw chat` | Connect a TUI chat session to the running server |
| `tclaw secret set NAME value` | Store a secret in the OS keychain |
| `tclaw secret get NAME` | Retrieve a secret |
| `tclaw secret delete NAME` | Remove a secret |
| `tclaw build` | Build all binaries to `bin/` |
| `tclaw install` | Install binaries to `$GOPATH/bin` |
| `tclaw tidy` | Tidy Go module dependencies |
| `tclaw deploy` | Build locally and deploy to Fly.io |
| `tclaw deploy secrets` | Push keychain secrets to Fly.io |
| `tclaw deploy suspend` | Spin down the Fly.io deployment |
| `tclaw deploy resume` | Spin up the Fly.io deployment |
| `tclaw deploy status` | Show Fly.io app status |
| `tclaw deploy logs` | Tail Fly.io app logs |
| `tclaw docker build` | Build Docker image |
| `tclaw docker up` | Start container via docker compose |
| `tclaw docker down` | Stop container |
| `tclaw docker chat` | Connect chat in running container |

## Configuration

See [`tclaw.example.yaml`](tclaw.example.yaml) for the full config reference.

Secrets use `${secret:NAME}` syntax — tries OS keychain first, falls back to env vars. After resolution, env vars are scrubbed from the process.

## Chat Commands

| Command | Effect |
|---------|--------|
| `new` / `reset` / `clear` / `delete` | Open reset menu (session, memories, project, everything) |
| `stop` | Cancel the active turn |
| `login` | Start interactive authentication |
| `auth` | Show authentication status |
| `compact` | Compact conversation context |
| `quit` / `exit` | Disconnect chat client |

## License

Private.
