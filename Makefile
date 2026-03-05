.PHONY: agent chat agent-dev chat-dev build clean tidy

# Run the agent daemon — logs appear here.
agent:
	@echo "→ starting agent (logs below)..."
	@go run .

# Connect a chat session to the running agent.
chat:
	@echo "→ connecting to agent..."
	@go run ./cmd/chat

# Hot-reload versions — restart on any .go change.
agent-dev:
	@echo "→ starting agent (hot reload)..."
	@mkdir -p tmp
	@air -c .air.agent.toml

# chat is stateless so hot-reload via air isn't useful — air doesn't pass
# stdin through to child processes. Just build fresh and run directly.
chat-dev:
	@echo "→ connecting to agent..."
	@go run ./cmd/chat

build:
	@echo "→ building..."
	@go build -o bin/agent .
	@go build -o bin/chat ./cmd/chat
	@echo "✓ bin/agent  bin/chat"

clean:
	@echo "→ cleaning..."
	@rm -rf bin/
	@echo "✓ done"

tidy:
	@echo "→ tidying dependencies..."
	@go mod tidy
	@echo "✓ done"
