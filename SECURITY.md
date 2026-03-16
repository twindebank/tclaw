# Security Policy

## Reporting Vulnerabilities

If you discover a security vulnerability in tclaw, please report it through [GitHub's private vulnerability reporting](https://github.com/twindebank/tclaw/security/advisories/new).

Do not open a public issue for security vulnerabilities.

## Scope

The following areas are in scope:

- **Authentication** — API key handling, OAuth flows, setup token management
- **Secret management** — encrypted storage, key derivation, env var scrubbing
- **Sandbox escapes** — bubblewrap mount namespace isolation, environment allowlist
- **MCP tool security** — tool permission gating, input validation
- **Remote MCP SSRF** — URL validation, private IP blocking

## Response

I'll acknowledge reports within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.
