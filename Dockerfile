FROM golang:1.26-bookworm AS builder

ARG COMMIT

WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY . .

# Fail fast if required build args are missing rather than deploying a broken build.
RUN test -n "${COMMIT}" || (echo "ERROR: COMMIT build arg is required" && false)

RUN CGO_ENABLED=0 go build -ldflags "-X tclaw/version.Commit=${COMMIT}" -o /bin/tclaw .

# ---

FROM node:22-bookworm-slim

# TLS CA certs (needed for outbound HTTPS, e.g. Telegram API),
# bubblewrap for subprocess filesystem sandboxing,
# git for dev workflow (clone/fetch/commit/push),
# and curl for fetching gh CLI.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates bubblewrap git curl && rm -rf /var/lib/apt/lists/*

# GitHub CLI — needed by dev_end for PR creation.
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh && rm -rf /var/lib/apt/lists/*

# Fly.io CLI — needed by deploy tool for `fly deploy`.
RUN curl -fsSL https://fly.io/install.sh | sh
ENV PATH="/root/.fly/bin:${PATH}"

# Install claude CLI and Google Workspace CLI globally.
RUN npm install -g @anthropic-ai/claude-code @googleworkspace/cli

# Copy the Go binary.
COPY --from=builder /bin/tclaw /usr/local/bin/tclaw

# Seed config for first boot. Written from the TCLAW_YAML GitHub secret in CI,
# or present locally for local deploys. Only used when the persistent volume
# has no config yet — never overwrites the live runtime config.
COPY tclaw.yaml /etc/tclaw/tclaw.yaml

# Persistent volume at /data holds all per-user state.
VOLUME ["/data"]

ENV CLAUDECODE=""
ENV CLAUDE_CODE_ENTRYPOINT=""

ENTRYPOINT ["tclaw", "serve", "--config", "/etc/tclaw/tclaw.yaml", "--env", "prod"]
