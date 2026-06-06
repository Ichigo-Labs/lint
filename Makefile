# lint — build/test convenience targets.
# Requires Go (with CGO enabled) and a C compiler, since the Tree-sitter
# grammars are compiled in.

BIN      := lint
PKG      := ./cmd/lint
VERSION  ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS  := -X github.com/ichigo-labs/lint/internal/cli.version=$(VERSION)

export CGO_ENABLED := 1

.PHONY: all build install test test-race vet fmt selftest clean

all: build

build: ## Build the lint binary
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

install: ## Install lint into GOBIN
	go install -ldflags "$(LDFLAGS)" $(PKG)

test: ## Run Go unit tests
	go test ./...

test-race: ## Run Go unit tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the source tree
	gofmt -w ./cmd ./internal

selftest: build ## Run the inline tests embedded in every discovered rule
	./$(BIN) test

clean:
	rm -f $(BIN)
