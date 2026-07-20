# ArcLoom Enterprise (`ee/`)

Everything in this directory is licensed under [`ee/LICENSE`](LICENSE) (the
ArcLoom Enterprise Edition License), **not** the repository's Apache-2.0 license.

This directory is the home for ArcLoom's Enterprise Features — functionality that
is enabled only when a valid license key is installed. See the top-level
[LICENSING.md](../LICENSING.md) for the open-core split and the current list of
gated features.

## How the gate is enforced

The license verifier and feature/seat policy live in `internal/license` (part of
the Apache core — verifying a signature is not itself an enterprise feature). The
server checks it at the enforcement points:

- SSO/OIDC provider is only stood up when the license grants `sso`
  (`internal/api/settings.go`).
- Creating users past the community seat limit requires `multi_user`
  (`internal/api/users.go`).
- Enabling **scheduled** drift detection requires `drift`
  (`internal/api/drift.go`); the scheduler also skips checks without it.

Per `ee/LICENSE`, the license gate must not be circumvented, disabled, or
bypassed to use Enterprise Features without a valid license.

Enterprise feature *implementations* will migrate into this directory over time;
today it defines the licensing boundary and terms.
