FROM golang:1.26-bookworm AS builder

ARG COMMIT=""
ARG GO_BUILD_PARALLEL=""

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# GO_BUILD_PARALLEL limits build parallelism via -p flag. Set to "2" on
# memory-constrained remote builders (Depot) where gotd/td/tg OOM-kills
# the compiler. Leave empty for local builds to use all cores.
RUN CGO_ENABLED=0 go build ${GO_BUILD_PARALLEL:+-p $GO_BUILD_PARALLEL} -ldflags "-X tclaw/version.Commit=${COMMIT}" -o /bin/tclaw .

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

# tclaw.yaml is gitignored — the deploy tool copies it into the build context.
# Fail the build if it's missing rather than falling back to the example config,
# which lacks the prod environment and causes a crash loop.
COPY tclaw.yaml /etc/tclaw/tclaw.yaml

# Persistent volume at /data holds all per-user state (store, home dirs, etc.).
VOLUME ["/data"]

ENV CLAUDECODE=""
ENV CLAUDE_CODE_ENTRYPOINT=""

ENTRYPOINT ["tclaw", "serve", "--config", "/etc/tclaw/tclaw.yaml", "--env", "prod"]
