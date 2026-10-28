# melti-cli

A Go command-line client for [Meltiply](https://github.com/MeltiplyOrg/meltiply).
It calls the same HTTP interface as the dashboard; the server owns Workspace
permissions, Post validation, persistence, and publishing.

## Backend prerequisite

Browser authorization requires
[Meltiply #79](https://github.com/MeltiplyOrg/meltiply/issues/79). Until the
installation implements that protocol, CLI login cannot complete. This repository
implements the client, not the backend changes. The client has been exercised
against a local HTTP protocol fixture, not a deployed Meltiply installation.

## Build

Requires Go 1.22 or newer:

```sh
go build -o bin/melti-cli ./cmd/melti-cli
./bin/melti-cli --help
```

The executable is `melti-cli`. The Go module retains the existing GitHub repository
address, `github.com/MeltiplyOrg/meltiply-cli`; the repository has not been renamed.

## Connect and authorize

Use your installation's application **origin**, without `/api/v1` or another path:

```sh
./bin/melti-cli auth login --server https://your-meltiply.example
./bin/melti-cli auth status
```

Login prints a matching code and opens the installation's browser approval page.
Sign in there, compare the code, and explicitly approve access. Passwords and
second-factor codes stay in the browser. For a remote terminal, use `--no-browser`
and open the printed URL yourself. Repeating login reuses an already-valid session.

The CLI saves its separate session in the OS credential store: macOS Keychain,
Windows Credential Manager, or Linux Secret Service. Linux requires a running,
unlocked Secret Service/keyring and D-Bus session. A credential-store failure
never silently falls back to plaintext. On a machine without a keyring, explicitly
opt in:

```sh
./bin/melti-cli auth login --server https://your-meltiply.example \
  --no-browser --insecure-storage
```

This stores the session in an owner-only (`0600`) **plaintext** file, not encrypted
storage. File storage remains selected only for that installation; other origins
still default to the OS keyring. Protect the configuration directory and do not
share it with untrusted users. A live existing login is reused without changing
its storage; log out before choosing another storage mode.

Each authenticated command obtains a fresh short-lived API JWT from the saved
session. A replacement session credential is saved when the server supplies one.
Only the JWT goes to core API routes. Credentials are scoped to the canonical
installation origin, and credential-bearing HTTP redirects are refused.

```sh
./bin/melti-cli auth logout
```

Logout revokes this CLI session before removing the local credential; it does not
sign out the browser. If revocation fails, the credential remains so logout can be
retried. Expired or revoked sessions require login again.

## Posts

Commands currently support a bounded page of Posts and creation of text-only
Posts in the draft lifecycle state:

```sh
./bin/melti-cli posts list --limit 20 --offset 0
./bin/melti-cli posts list --brand-profile <brand-profile-uuid> --status draft
./bin/melti-cli posts create --brand-profile <brand-profile-uuid> \
  --text "Hello from the terminal"
```

Use a Brand Profile UUID from your installation. Listing accepts `--limit` from
1–100, `--offset` from 0–1000000, and `--status draft|active`. Creation does **not**
publish or schedule the Post and needs no connected Platform account.

Every creation sends an idempotency key. To repeat one logical action safely,
supply the same key and unchanged input:

```sh
./bin/melti-cli posts create --brand-profile <brand-profile-uuid> \
  --text "Hello from the terminal" --idempotency-key publish-plan-draft-001
```

If the key is omitted, the CLI generates a random UUID for that invocation. A
failed mutation includes its key in the JSON error so an uncertain action can be
retried with that identity. Running the command again without reusing the key is
a new action and may create another Post. The CLI does not automatically retry
mutations.

## Configuration and output

- Server precedence: explicit `--server`, then `MELTI_SERVER`, then the last
  successfully saved login's origin. No production endpoint is assumed.
- Configuration directory precedence: `--config-dir`, then `MELTI_CONFIG_DIR`,
  then the OS user configuration directory's `melti-cli` subdirectory.
- HTTP is allowed only for loopback development, such as
  `http://127.0.0.1:3000`; remote installations require HTTPS.
- Successful command data is JSON on stdout. Login instructions and plaintext
  storage warnings go to stderr. Failed commands write one JSON error envelope
  to stderr, including backend error code, HTTP status, and request ID when
  available. Cancellation returns exit code 130; invalid flags/arguments return
  2; other failures return 1.
- Requests have a 30-second timeout and an 8 MiB response limit. Device polling
  honors the server's interval, pending/slow-down responses, and expiration.
- Shell completions are available through `melti-cli completion --help`.

## Development

```sh
go build ./...
go vet ./...
go test -race ./...
```

Tests exercise the public command interface against local HTTP servers with an
isolated credential-store adapter, including browser approval, pending/denied/
expired grants, polling backoff, session replacement, logout failure, credential
isolation, redirect blocking, pagination, and idempotent Post creation. Explicit
plaintext storage is also exercised through separate command instances. These
tests do not prove a live backend deployment or an OS keyring implementation.

## Architecture research

The research documents describe possible future capabilities, not the current
command surface:

- [Agent-first architecture research](./RESEARCH_AGENT_FIRST_CLI.md)
- [Open-source ecosystem research](./ECOSYSTEM_RESEARCH.md)

MCP, `tools`, `schema`, intent search, `--jq`, `--body`, `--dry-run`, uploads,
scheduling, and publishing commands are not implemented.
