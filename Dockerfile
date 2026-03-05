FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/tclaw . && \
    CGO_ENABLED=0 go build -o /bin/tclaw-chat ./cmd/chat

# ---

FROM node:22-bookworm-slim

# Install claude CLI globally.
RUN npm install -g @anthropic-ai/claude-code

# Copy the Go binaries.
COPY --from=builder /bin/tclaw /usr/local/bin/tclaw
COPY --from=builder /bin/tclaw-chat /usr/local/bin/tclaw-chat

# Persistent volumes:
#   /data/store  — our key-value store (session IDs, etc.)
#   /root/.claude — Claude Code's own config, sessions, and conversation history
VOLUME ["/data/store", "/root/.claude"]

# The unix socket lives here by default.
VOLUME ["/tmp"]

ENV CLAUDECODE=""
ENV CLAUDE_CODE_ENTRYPOINT=""

ENTRYPOINT ["tclaw"]
