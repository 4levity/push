GO      ?= go
BIN     ?= build/push
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOBIN   := $(shell $(GO) env GOPATH)/bin
GOLINT  := $(shell command -v golangci-lint 2>/dev/null)

.PHONY: build test vet lint clean

build: ## Build a static binary into build/push
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) .

test: ## Run all tests with the race detector
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint: ## Run golangci-lint (installs it to GOPATH/bin if missing)
	@if [ -n "$(GOLINT)" ]; then \
		"$(GOLINT)" run ./...; \
	elif [ -x "$(GOBIN)/golangci-lint" ]; then \
		"$(GOBIN)/golangci-lint" run ./...; \
	else \
		echo "golangci-lint not found; installing v2.15.0 to $(GOBIN)..."; \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0; \
		"$(GOBIN)/golangci-lint" run ./...; \
	fi

clean:
	rm -rf build
