# Phase 3 Step 7 (G1) -- atlax-tools unit tests

**Date:** 2026-04-16
**Branch:** `test/atlax-tools-coverage`
**Base:** `origin/main` at commit `cb651a9`
**Repo:** `github.com/atlasshare/atlax-tools`
**Scope:** Unit tests only. No production code changes.

## Objective

Bring per-package coverage on four core `internal/` packages to at least
80%, wire a Makefile coverage gate, and document any testability
refactors.

## Coverage results

| Package           | Before | After | Test cases* |
|-------------------|--------|-------|-------------|
| `internal/config` |  0.0%  | 94.7% | 31          |
| `internal/certs`  |  0.0%  | 83.1% | 20          |
| `internal/tui`    |  0.0%  | 97.0% | 58          |
| `internal/caddy`  |  0.0%  | 94.9% | 16          |
| **Weighted total** | **0.0%** | **92.3%** | **125** |

_\* Includes table-driven subtests._

All four packages exceed the 80% gate. 125 passing test cases total
across six new `*_test.go` files.

## Files added

- `internal/caddy/manager_test.go` -- Caddyfile rendering,
  service block helpers, and `AppendToFile` (dry run, create, append
  to existing, duplicate domain rejection).
- `internal/config/manager_test.go` -- YAML round trip,
  `AddCustomer`/`AddPort`/`AddService` happy paths and all error
  branches, `FindNextPort` including exhaustion and inverted range,
  `CustomerByID` pointer semantics, `BackupFile`, and defaults.
- `internal/tui/checklist_test.go` -- state transitions (pending ->
  in-progress -> done / failed / skipped), checkpoint save and resume
  semantics (done carries over, failed resets to pending for retry),
  malformed checkpoint tolerance, and cleanup.
- `internal/tui/prompt_test.go` -- drives the full prompt surface via
  an in-package `bufio.Reader` swap: `Ask`, `AskRequired`, `AskInt`,
  `AskIntRange`, `AskPath`, `Confirm`, `Select`, `SelectString`,
  `AskMultiSelect`, `AskPassword`.
- `internal/tui/style_test.go` -- every styled printer plus the
  `SetNoColor` toggle via captured stdout.
- `internal/certs/generator_test.go` -- pure-Go helpers
  (`DefaultOpts`, `DetectBackend`, `Backend.String`, `isIPAddress`,
  `concatFiles`, `runCmd`), dry-run paths for `GenerateFullPKI` and
  `IssueAgentCert`, error-path tests for `IssueAgentCert`, and a
  shared real PKI generated once per test binary that drives PEM,
  `x509`, `NotAfter`, serial, SAN, EKU, chain ordering, and key-file
  permission assertions for every cert tier.
- `docs/step-reports/phase3-step7-g1-report.md` (this file)

## Files modified

- `Makefile` -- added `test-coverage` target with the 80% gate required by
  the plan, and listed it in `help`.
- `.gitignore` -- added `coverage.out`, `*.cover`, `*.coverprofile` so
  future coverage runs do not pollute the working tree.
- `go.mod` / `go.sum` -- `github.com/stretchr/testify` promoted from
  indirect to direct dependency. Version `v1.11.1`.

## Production code refactors

**None.** Every target function was exercised through its existing
exported API or through the in-package `var reader` indirection that
already existed in `internal/tui/prompt.go`. The `certs` package private
helpers (`generateRootCA`, `generateRelayCA`, `generateCustomerCA`,
`generateRelayCert`, `generateAgentCert`, `buildChains`,
`buildAgentChain`, `concatFiles`, `isIPAddress`, `runCmd`) were all
covered indirectly through `GenerateFullPKI` and `IssueAgentCert` plus a
handful of direct unit tests for the pure helpers.

## Test design notes

### `internal/caddy`

Table-driven `Block.Render` tests assert both structural invariants
(brace opening/closing, directive ordering) and content (expected
directives appear). File operations use `t.TempDir()`.

### `internal/config`

Round-trip tests marshal each config to a tempdir YAML file, read it
back, and `assert.Equal` on the whole struct -- this catches any
mismatch between yaml tags and struct fields. `AddPort` and
`AddCustomer` tests cover happy paths plus every error branch
(duplicate customer, port already allocated on same and different
customers, duplicate service name, unknown customer). `FindNextPort`
tests include empty config, contiguous allocation, non-contiguous
holes, exhausted range, and inverted range.

### `internal/tui`

The `prompt.go` functions read from a package-level `bufio.Reader` that
already existed as a `var`. Tests swap it for a `strings.NewReader`
under a mutex, so no production change was needed. `captureStdout`
redirects `os.Stdout` through a pipe (also under a mutex) so the
assertion-friendly style printers can be exercised without polluting
the test runner output. Checklist tests redirect `$HOME` to a tempdir
via `t.Setenv` so checkpoint writes never touch the real user's home.

### `internal/certs`

The production code shells out to `openssl` (or `step`). To produce
real x509 output for PEM/NotAfter/Serial verification the tests:

1. Probe the available OpenSSL on PATH.
2. If it is LibreSSL (Apple's default `/usr/bin/openssl`), search for a
   Homebrew `openssl@3` or `openssl@1.1` and prepend it to PATH.
3. If no real OpenSSL is found at all (or `-short` is in effect), skip
   the real-cert tests with `t.Skip` while the pure-Go helpers are
   still covered.

The real PKI is generated **once** per test binary via a `sync.Once`
and reused across the six real-cert subtests, so the expensive
RSA-4096 key generation happens once (~3-5 s on Apple Silicon with
Homebrew OpenSSL 3.6).

The PATH shim is a **test-environment concession** for macOS
developers. It does not modify production behaviour and has no
runtime effect outside `go test`.

## Bugs / issues found during this work

### 1. LibreSSL incompatibility with production cert generation (environmental, low severity)

`openssl x509 -req -CAcreateserial` combined with the
`authorityKeyIdentifier=keyid:always,issuer` extension used in
`generateRelayCA`, `generateCustomerCA`, and the leaf cert paths is
rejected by LibreSSL 3.3 (Apple's default `/usr/bin/openssl`):

```
X509 V3 routines:func(4095):reason(123):.../x509_akey.c:195
```

This is **not a new bug** and does not affect production (atlax-tools
targets Linux hosts where OpenSSL 3.x is available). It is surfaced
here only to document that local macOS `make certs-dev` runs on a
stock system will fail unless Homebrew OpenSSL is put ahead of Apple's
on PATH. Reporting it out of scope per CLAUDE.md "issues outside your
current domain" protocol is unnecessary because it is an environmental
note rather than an atlax-tools bug.

### 2. Pre-existing lint warnings on main (pre-existing, not introduced by this branch)

Running `GOWORK=off golangci-lint run ./...` on `origin/main`
(`cb651a9`) reports 24 pre-existing issues: 17 errcheck, 1 ineffassign,
6 staticcheck -- across `internal/cli/`, `internal/firewall/`,
`internal/logger/`, `internal/platform/`, `internal/caddy/manager.go`.
This branch inherits the same findings (22-24 depending on reader
version); **no new lint warnings** were introduced by any of the test
files added here. Per CLAUDE.md, these are flagged for separate triage
rather than fixed in this branch.

### 3. Generated private key file permissions (observation, not a bug)

During testing we observed that `openssl req` (both Homebrew OpenSSL
3.6 and LibreSSL 3.3) emits private key files at `0600` on POSIX
systems -- matching the workspace security convention. The tests
assert the slightly weaker invariant "no group or other bits set"
(`perm & 0o077 == 0`) so they remain stable across umask variations
encountered in CI environments, while still catching any accidental
widening (e.g. 0644).

This is an observation, not an action item. No production change is
needed -- but if the project wants a stronger guarantee it could add
an explicit `os.Chmod(keyPath, 0o600)` after each `openssl req` call
in `generator.go`. That is out of scope for this test-only step.

### 4. No bugs found in the covered production code

All tests passed on the existing implementations. No mutation was
required. `isIPAddress` is intentionally permissive in its current
form (accepts anything composed of digits, dots, colons) -- that is
documented inline in the test.

## Verification gates

| Gate                                 | Status |
|--------------------------------------|--------|
| `go vet ./...`                       | PASS (no output) |
| `go build ./...`                     | PASS |
| `make test` (`-race -count=1`)       | PASS (4 covered + existing) |
| `make test-coverage` (80% gate)      | PASS (92.3% total) |
| `make lint` (test files only)        | PASS (0 new issues) |
| Deterministic (no flakes, no wall-clock) | PASS |

## Commits on this branch

See `git log origin/main..test/atlax-tools-coverage`.

## Not done (explicit non-goals)

- No production code changes (per the plan).
- No refactor of the cert backend to use a pure-Go library. The plan's
  Step 7 G1 is scoped to unit tests; swapping the cert generator out
  of shell-out is a larger refactor best treated as a separate task.
- No Makefile integration of `test-coverage` into `test` -- kept as a
  separate target so developers can iterate quickly with `make test`
  without paying the cert-generation cost on every invocation. CI
  should invoke `make test-coverage` as the gating check.
