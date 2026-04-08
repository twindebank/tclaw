# Contributing

## Development Setup

Follow the [Quick Start](README.md#quick-start) in the README to get a local instance running, then make your changes.

## Running Tests

```bash
go test ./...
```

## Code Style

See [docs/go-patterns.md](docs/go-patterns.md) for conventions on comments, error handling, testing, and naming.

Key points:

- Comments explain "why" not "what"
- Never ignore errors -- handle or return them
- Use `require` from testify (not assert, not suites)
- Prefer param structs over multiple parameters
- Don't abbreviate names
