.PHONY: build check test

build:
	go build -trimpath -o bin/melti-cli ./cmd/melti-cli

check:
	go build ./...
	go vet ./...

test:
	go test -race ./...
