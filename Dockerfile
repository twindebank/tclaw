FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X tclaw/version.Commit=$(git rev-parse --short HEAD)" -o /bin/tclaw .

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

# Fly.io CLI — needed by deploy tool for `fly deploy --remote-only`.
RUN curl -fsSL https://fly.io/install.sh | sh
ENV PATH="/root/.fly/bin:${PATH}"

# Install claude CLI and Google Workspace CLI globally.
RUN npm install -g @anthropic-ai/claude-code @googleworkspace/cli

# Copy the Go binaries.
COPY --from=builder /bin/tclaw /usr/local/bin/tclaw

# Config file — multi-env, prod section selected at runtime.
COPY tclaw.yaml /etc/tclaw/tclaw.yaml

# Persistent volume at /data holds all per-user state (store, home dirs, etc.).
VOLUME ["/data"]

ENV CLAUDECODE=""
ENV CLAUDE_CODE_ENTRYPOINT=""

ENTRYPOINT ["tclaw", "serve", "--config", "/etc/tclaw/tclaw.yaml", "--env", "prod"]
