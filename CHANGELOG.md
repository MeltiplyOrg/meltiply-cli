# Changelog

## Unreleased

- Name the project and executable `melti-cli`; retain the existing GitHub module address.
- Add browser-device login against the protocol specified in [Meltiply #79](https://github.com/MeltiplyOrg/meltiply/issues/79), current-member status, and server-side logout.
- Store installation-scoped sessions in the OS credential store, with explicitly opted-in owner-only plaintext storage for machines without a keyring.
- Obtain a fresh API JWT per command and retain replacement session credentials.
- Add bounded Post listing and idempotent text-only draft Post creation.
- Add JSON success/error output, cancellation, request limits, and credential-safe redirect handling.
- Add public-command HTTP tests and distinguish implemented behavior from architecture research.
