REPO_DIR := $(shell pwd)
SHELL_RC := $(HOME)/.zshrc
DEV_MARKER := \# --- tclaw dev (do not edit) ---
DEV_END    := \# --- end tclaw dev ---

.PHONY: install install-dev uninstall

# Install compiled binaries to $GOPATH/bin.
install:
	@go install .
	@cd cmd/chat && go install .
	@echo "✓ installed tclaw, tclaw-chat to GOPATH"

# Install shell functions that run from source (changes reflected immediately).
install-dev:
	@if grep -q "tclaw dev (do not edit)" "$(SHELL_RC)" 2>/dev/null; then \
		echo "✓ already installed (dev) in $(SHELL_RC)"; \
	else \
		echo "" >> "$(SHELL_RC)"; \
		echo "$(DEV_MARKER)" >> "$(SHELL_RC)"; \
		echo 'tclaw() { (cd "$(REPO_DIR)" && go run . "$$@") }' >> "$(SHELL_RC)"; \
		echo "$(DEV_END)" >> "$(SHELL_RC)"; \
		echo "✓ installed tclaw shell function in $(SHELL_RC)"; \
		echo "  run: source $(SHELL_RC)"; \
	fi

# Remove everything — binaries and shell functions. Safe if either is missing.
uninstall:
	@rm -f "$$(go env GOPATH)/bin/tclaw" "$$(go env GOPATH)/bin/tclaw-chat" 2>/dev/null; \
	echo "✓ removed binaries (if present)"
	@if grep -q "tclaw dev (do not edit)" "$(SHELL_RC)" 2>/dev/null; then \
		sed -i '' '/tclaw dev (do not edit)/,/end tclaw dev/d' "$(SHELL_RC)"; \
		echo "✓ removed shell functions from $(SHELL_RC)"; \
		echo "  run: source $(SHELL_RC)"; \
	else \
		echo "✓ no shell functions to remove"; \
	fi
