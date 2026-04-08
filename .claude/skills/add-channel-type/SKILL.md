---
name: add-channel-type
description: Scaffold a new channel type with transport, package, and registry entry
allowed-tools: Bash, Read, Edit, Write, Glob, Grep
---

Scaffold a new channel type under `internal/channel/`. Ask the user for:

1. **Channel type name** — lowercase (e.g. `slack`, `discord`, `whatsapp`)
2. **Markup format** — `MarkupMarkdown` or `MarkupHTML`
3. **Needs provisioner?** — for ephemeral channel auto-setup (e.g. creating bots)
4. **Needs platform state?** — for persistent platform-specific data (e.g. group IDs)
5. **Config fields** — what goes in tclaw.yaml for this channel type (e.g. token, webhook URL)

## Reference patterns

Before writing any code, read these files to match the exact patterns:

- `internal/channel/socketchannel/package.go` — minimal package (no provisioner)
- `internal/channel/telegramchannel/package.go` — package with provisioner
- `internal/channel/channelpkg/channelpkg.go` — the `Package` interface and `BuildParams`
- `internal/channel/channel.go` — `Channel` interface, `ChannelType` constants, `Markup` types
- `internal/channel/platform_state.go` — platform state types
- `internal/channel/all/all.go` — where packages are registered
- `internal/config/config.go` — config structs for channel types

Also read `docs/go-patterns.md` for code style rules.

## Files to create/modify

For a channel type named `{name}`:

### `internal/channel/{name}channel/{name}.go`
- Transport struct implementing `channel.Channel` interface
- Methods: `Send()`, `Receive()`, `Close()`, `Markup()`, `StatusWrap()`, `SplitStatusMessages()`, `FormattingInstructions()`

### `internal/channel/{name}channel/package.go`
- Struct implementing `channelpkg.Package`
- `Type()` returns `channel.Type{Name}` constant
- `Build(ctx, params)` constructs the transport from config
- `Provisioner()` returns provisioner or `nil`

### Modifications to existing files

1. **`internal/channel/channel.go`** — add `Type{Name} ChannelType = "{name}"` constant
2. **`internal/channel/platform_state.go`** — add `Platform{Name} PlatformType = "{name}"` if needs platform state
3. **`internal/channel/all/all.go`** — add import and register the package in `NewRegistry()`
4. **`internal/config/config.go`** — add `{Name}ChannelConfig` struct and `{Name} *{Name}ChannelConfig` field to `Channel` struct

## Verification

Run `go build ./...` to verify compilation.

## Do NOT:
- Put provisioner logic in the transport file — keep it in a separate `provisioner.go` if needed
- Skip the `ChannelType` constant — always add it to `channel.go`
- Abbreviate package or variable names
