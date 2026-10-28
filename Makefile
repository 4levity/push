GO      ?= go
BIN     ?= build/push
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet clean

build: ## Build a static binary into build/push
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) .

test: ## Run all tests with the race detector
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf build
