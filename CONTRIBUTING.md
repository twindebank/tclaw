# Contributing

## Development Setup

```bash
# Clone the repo
git clone https://github.com/twindebank/tclaw.git
cd tclaw

# Install in dev mode (runs from source)
make install-dev
source ~/.zshrc

# Set your API key
tclaw secret set ANTHROPIC_API_KEY sk-ant-...

# Copy the example config
cp tclaw.example.yaml tclaw.yaml

# Start the server
tclaw serve

# In another terminal, connect the chat client
tclaw chat
```

## Running Tests

```bash
go test ./...
```

## Code Style

See [docs/go-patterns.md](docs/go-patterns.md) for conventions on comments, error handling, testing, and naming.

Key points:

- Comments explain "why" not "what"
- Never ignore errors — handle or return them
- Use `require` from testify (not assert, not suites)
- Prefer param structs over multiple parameters
- Don't abbreviate names
