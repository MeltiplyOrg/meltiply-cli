VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Pinned generator: the vendored spec plus this version fully determine the
# generated types, so any drift is reproducible.
OAPI_CODEGEN_VERSION := v2.8.1

.PHONY: build check test lint fmt generate verify-generated

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/melti-cli ./cmd/melti-cli

check:
	go build ./...
	go vet ./...

test:
	go test -race -shuffle=on -count=1 -coverpkg=./internal/cli -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1
	@rm -f coverage.out

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

# Regenerates internal/api from the vendored Meltiply contract. Run this rather
# than hand-editing generated files when an operation's wire shape changes.
generate:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
		-config spec/oapi-codegen.yaml spec/openapi.yaml

# Fails when the checked-in generated types no longer match the spec. Used by CI
# so a contract change cannot land as silently stale Go code. The output is
# regenerated and compared byte for byte against the committed file, so the gate
# works whether the file is tracked, staged, or new, and never leaves the
# working tree modified.
verify-generated:
	@tmp="$$(mktemp)"; \
	cp internal/api/types.gen.go "$$tmp"; \
	if ! $(MAKE) --no-print-directory generate >/dev/null; then \
		cp "$$tmp" internal/api/types.gen.go; rm -f "$$tmp"; \
		echo "code generation failed"; exit 1; \
	fi; \
	if ! cmp -s "$$tmp" internal/api/types.gen.go; then \
		cp "$$tmp" internal/api/types.gen.go; rm -f "$$tmp"; \
		echo "internal/api is stale: run 'make generate' and commit the result"; \
		exit 1; \
	fi; \
	rm -f "$$tmp"
