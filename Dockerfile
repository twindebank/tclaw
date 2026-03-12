FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/tclaw .

# ---

FROM node:22-bookworm-slim

# TLS CA certs (needed for outbound HTTPS, e.g. Telegram API).
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

# Install claude CLI and Google Workspace CLI globally.
RUN npm install -g @anthropic-ai/claude-code @googleworkspace/cli

# Copy the Go binaries.
COPY --from=builder /bin/tclaw /usr/local/bin/tclaw

# Deploy config — secrets resolved from env vars at runtime.
COPY tclaw.deploy.yaml /etc/tclaw/tclaw.yaml

# Persistent volume at /data holds all per-user state (store, home dirs, etc.).
VOLUME ["/data"]

ENV CLAUDECODE=""
ENV CLAUDE_CODE_ENTRYPOINT=""

ENTRYPOINT ["tclaw", "serve", "--config", "/etc/tclaw/tclaw.yaml"]
