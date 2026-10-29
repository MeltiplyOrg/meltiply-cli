# melti-cli

[![CI](https://github.com/MeltiplyOrg/meltiply-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/MeltiplyOrg/meltiply-cli/actions/workflows/ci.yml)
[![CodeQL](https://github.com/MeltiplyOrg/meltiply-cli/actions/workflows/codeql.yml/badge.svg)](https://github.com/MeltiplyOrg/meltiply-cli/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/MeltiplyOrg/meltiply-cli/badge)](https://scorecard.dev/viewer/?uri=github.com/MeltiplyOrg/meltiply-cli)

A Go command-line client for [Meltiply](https://github.com/MeltiplyOrg/meltiply).
It calls the same HTTP interface as the dashboard; the server owns Workspace
permissions, Post validation, persistence, and publishing.

## Backend support

Browser authorization is implemented by Meltiply
([PR #83](https://github.com/MeltiplyOrg/meltiply/pull/83), closing
[#79](https://github.com/MeltiplyOrg/meltiply/issues/79)); the observable
contract is documented in
[`docs/device-authorization.md`](https://github.com/MeltiplyOrg/meltiply/blob/master/docs/device-authorization.md).
Installations older than that change cannot complete CLI login.

The complete flow — device code, browser consent, session issuance, JWT
exchange, draft creation, listing, and logout revocation — has been exercised
end to end against a locally running Meltiply API Worker at the merged commit.
It has not been exercised against a deployed production installation, and CI
uses local HTTP fixtures so it needs no live backend.

## Build

Requires Go 1.22 or newer:

```sh
go build -o bin/melti-cli ./cmd/melti-cli
./bin/melti-cli --help
```

The executable is `melti-cli`. The Go module retains the existing GitHub repository
address, `github.com/MeltiplyOrg/meltiply-cli`; the repository has not been renamed.

`make build` injects version, commit, and build date from Git; release archives are
built by GoReleaser. Inspect the running binary without contacting an installation:

```sh
./bin/melti-cli version
```

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

This stores the session in a **plaintext** file, not encrypted storage. On POSIX
systems the CLI requires owner-only `0600` permissions; on Windows the file
inherits the user profile directory's ACL. File storage remains selected only
for that installation; other origins
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
  honors the server's interval, `authorization_pending`, `slow_down`, and
  `Retry-After` on rate limiting, and stops at the grant's expiration.
- Shell completions are available through `melti-cli completion --help`.

## Development

```sh
make build        # compile with version metadata
make check        # build all packages and run go vet
make test         # race-enabled, shuffled tests
make lint         # golangci-lint (requires it on PATH)
make fmt          # apply gofumpt/goimports (requires golangci-lint)
```

Tests exercise the public command interface against local HTTP servers with an
isolated credential-store adapter, including browser approval, pending/denied/
expired grants, polling backoff, session replacement, logout failure, credential
isolation, redirect blocking, pagination, and idempotent Post creation. Explicit
plaintext storage is also exercised through separate command instances. These
tests do not prove a live backend deployment or an OS keyring implementation.

### Continuous integration

CI runs on every push to `main`, pull request, and merge-queue entry. Security
workflows run on pushes, pull requests, and a weekly schedule. Together they
provide:

- **Tests:** Go 1.22 through the current stable release, race detector, shuffled
  order, atomic coverage, and a 70% total statement-coverage floor. Coverage is
  uploaded to Codecov and retained as a build artifact.
- **Quality:** `go vet`, module tidiness, `go build`, golangci-lint (including
  gosec), and the repository's gofumpt/goimports formatting.
- **Workflow and docs:** `actionlint` validates every workflow; Lychee checks
  local Markdown links.
- **Portability:** cross-compiles every release target, and builds a GoReleaser
  snapshot with checksums and archives.
- **Security:** `govulncheck`, gosec SARIF upload, CodeQL (`security-extended`),
  dependency review on pull requests, and weekly OpenSSF Scorecard analysis.

Pushing a `v*` tag publishes Linux, macOS, and Windows archives for `amd64` and
`arm64` through GoReleaser, with a changelog and checksums. All workflows use
least-privilege permissions and pin linter and scanner versions in the workflow
files.

## Architecture research

The research documents describe possible future capabilities, not the current
command surface:

- [Agent-first architecture research](./RESEARCH_AGENT_FIRST_CLI.md)
- [Open-source ecosystem research](./ECOSYSTEM_RESEARCH.md)

MCP, `tools`, `schema`, intent search, `--jq`, `--body`, `--dry-run`, uploads,
scheduling, and publishing commands are not implemented.
