.PHONY: agent chat agent-dev chat-dev build clean tidy docker docker-agent docker-chat docker-down

# Run the agent daemon — logs appear here.
agent:
	@echo "→ starting agent (logs below)..."
	@go run .

# Connect a chat session to the running agent.
chat:
	@echo "→ connecting to agent..."
	@cd cmd/chat && go run .

# Hot-reload versions — restart on any .go change.
agent-dev:
	@echo "→ starting agent (hot reload)..."
	@mkdir -p tmp
	@air -c .air.agent.toml

# chat is stateless so hot-reload via air isn't useful — air doesn't pass
# stdin through to child processes. Just build fresh and run directly.
chat-dev:
	@echo "→ connecting to agent..."
	@cd cmd/chat && go run .

build:
	@echo "→ building..."
	@go build -o bin/agent .
	@cd cmd/chat && go build -o ../../bin/chat .
	@echo "✓ bin/agent  bin/chat"

clean:
	@echo "→ cleaning..."
	@rm -rf bin/
	@echo "✓ done"

tidy:
	@echo "→ tidying dependencies..."
	@go mod tidy
	@cd cmd/chat && go mod tidy
	@echo "✓ done"

docker:
	@echo "→ building docker image..."
	@docker build -t tclaw .

docker-agent:
	@echo "→ starting container..."
	@docker compose up --build -d

docker-down:
	@echo "→ stopping container..."
	@docker compose down

docker-chat:
	@docker compose exec agent tclaw-chat
