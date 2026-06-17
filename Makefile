# claude-sessions — TUI manager for Claude Code session transcripts.
# Installs to $BIN_DIR (defaults to ~/.local/bin).

BIN_DIR    ?= $(HOME)/.local/bin
LDFLAGS    := -s -w
GOFLAGS    := -trimpath

.PHONY: build install test cross clean

build: ## build for the host platform into ./bin
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/claude-sessions .

install: ## build + install into $(BIN_DIR)
	@mkdir -p "$(BIN_DIR)"
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o "$(BIN_DIR)/claude-sessions" .
	@echo "installed -> $(BIN_DIR)/claude-sessions"

test: ## run unit tests
	go test ./...

# cross-compile a single static binary for every platform you sync to.
cross: ## build dist/ binaries for linux+macos (amd64+arm64)
	@mkdir -p dist
	GOOS=linux  GOARCH=amd64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o dist/claude-sessions-linux-amd64 .
	GOOS=linux  GOARCH=arm64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o dist/claude-sessions-linux-arm64 .
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o dist/claude-sessions-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o dist/claude-sessions-darwin-arm64 .
	@ls -1 dist/

clean: ## remove build artifacts
	rm -rf bin dist
