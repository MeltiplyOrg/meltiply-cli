VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build check test lint fmt

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/melti-cli ./cmd/melti-cli

check:
	go build ./...
	go vet ./...

test:
	go test -race -shuffle=on -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...
