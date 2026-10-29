# Vendored Meltiply API contract

`openapi.yaml` is a verbatim copy of Meltiply's canonical contract
(`packages/contracts/openapi.yaml`). Do not edit it here; copy the backend file
and regenerate.

- **Source commit:** `7c4adb868e118dfd36ef87727a04a4b3b2cc6c92`
- **SHA-256:** `e8720aea65f95e5361b4ba520d29616e35ba8fdcbb0e15ef01c24658cfcf9eac`
- **Spec version:** `1.0.0-target.0`

## Updating

```sh
cp /path/to/meltiply/packages/contracts/openapi.yaml spec/openapi.yaml
make generate
go test ./...
```

Regenerating may break the build: that is the point. Generated types are the
compile-time link between this CLI and the server contract, so a renamed field
or changed enum fails here instead of silently shipping a malformed request.

## What is generated

`spec/oapi-codegen.yaml` limits generation to the operations the CLI calls.
Adding a command means adding its `operationId` there, then running
`make generate`. The device-authorization endpoints are deliberately absent:
they belong to Better Auth's plugin contract, not Meltiply's OpenAPI document.

CI runs `make verify-generated`, which fails when `internal/api` does not match
the vendored spec.

## Why OpenAPI 3.1

The backend contract is OpenAPI 3.1 (`type: [string, "null"]` etc.), but
`oapi-codegen` v2.8.1 — the last release whose *generated* runtime dependency
supports this module's Go 1.22 floor — does not fully implement 3.1. Generation
therefore relies on the subset that works and is validated by compiling the
result. If the contract starts using 3.1-only constructs that this generator
cannot express, the generator version and the Go floor must be raised together.
