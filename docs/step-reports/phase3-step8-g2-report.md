# Phase 3 Step 8 - G2: CI struct drift checker

Branch: `chore/config-struct-drift-check`
Base: `origin/main` at `cb651a9`
Worktree: `/tmp/atlax-tools-step8-g2/`

## Summary

Added a standalone Go tool at `cmd/driftcheck/` that detects YAML-tag
divergence between atlax-tools' local config mirror and the upstream
atlax community config. Wired it into CI as a new `drift-check` job.

Parses Go source files with `go/ast` and `go/parser` from the standard
library -- no regex, no external dependencies. Compares YAML-tagged
struct fields under a known struct-name mapping and exits non-zero on
drift.

## Struct-name mapping

The tools mirror uses different Go type names than community for most
config structs. The checker carries a mapping from community names to
tools names:

| Community                | Tools                       | Notes                                |
|--------------------------|-----------------------------|--------------------------------------|
| `RelayConfig`            | `RelayConfig`               | Same name                            |
| `AgentConfig`            | `AgentConfig`               | Same name                            |
| `ServerConfig`           | `RelayServer`               | Renamed                              |
| `TLSPaths`               | `RelayTLS`, `AgentTLS`      | One community struct maps to two     |
| `CustomerConfig`         | `Customer`                  | Renamed                              |
| `PortConfig`             | `PortConfig`                | Same name                            |
| `RelayConnection`        | `AgentRelay`                | Renamed                              |
| `LogConfig`              | `LoggingConfig`             | Renamed                              |
| `MetricsConfig`          | `MetricsConfig`             | Same name                            |
| `UpdateConfig`           | `UpdateConfig`              | Same name                            |
| `ServiceMapping`         | `ServiceConfig`             | Renamed                              |
| `RateLimitConfig`        | `RateLimitConfig`           | New in tools as part of this work    |

## Drift items found and fixed

The first run of the checker against community `pkg/config/config.go`
produced 9 errors and 1 missing struct. All were fixed by adding the
corresponding fields to the tools mirror:

### Pre-tool (known drift, commit `f1be7ef`)

- `RelayServer.AdminSocket` (yaml `admin_socket`) -- admin API unix socket
- `RelayServer.AgentListenAddr` (yaml `agent_listen_addr`) -- dedicated agent listener
- `RelayServer.StorePath` (yaml `store_path`) -- runtime port-mutation sidecar

### Post-tool (detected by driftcheck, commit `75d6e6f`)

- `Customer.MaxBandwidthMbps` (yaml `max_bandwidth_mbps`)
- `Customer.RateLimit` (yaml `rate_limit`)
- New struct `RateLimitConfig` with `RequestsPerSecond` and `Burst`
- `MetricsConfig.ListenAddr` (yaml `listen_addr`)
- `AgentRelay.InsecureSkipVerify` (yaml `insecure_skip_verify`)
- `AgentTLS.ClientCAFile` (yaml `client_ca_file`)
- `ServiceConfig.RelayPort` (yaml `relay_port`) plus reorder to match community field order
- `LoggingConfig.Output` (yaml `output`)
- `UpdateConfig.PublicKeyPath` (yaml `public_key_path`)

All new fields use `omitempty` so existing tools YAML consumers continue
to parse without requiring the new keys.

## Tools-only warnings (accepted)

Four fields exist in tools but not in community. These are not drift --
they are tools-only conveniences:

- `MetricsConfig.Path` (yaml `path`) -- tools-side metrics endpoint override
- `MetricsConfig.Prefix` (yaml `prefix`) -- tools-side metric name prefix
- `AgentRelay.ReconnectJitter` (yaml `reconnect_jitter`) -- jitter toggle
- `ServiceConfig.Description` (yaml `description`) -- docstring field

The checker reports these as `[WARN]` and exits 0 so they do not break CI.

## Type compatibility scope

Community uses `time.Duration` for several fields (`IdleTimeout`,
`ShutdownGracePeriod`, `CheckInterval`, `ReconnectInterval`,
`MaxReconnectBackoff`, `KeepaliveInterval`, `KeepaliveTimeout`) while
tools uses `string`. Both round-trip to the same YAML representation, so
the checker intentionally does NOT compare Go types -- only field names
and YAML tags.

This is documented in the tool's package comment and is a deliberate
design decision, not an oversight.

## CI workflow addition

Created `.github/workflows/ci.yml` (did not exist previously) with
three jobs:

- `test` -- `go test -race -count=1 ./...`
- `vet` -- `go vet ./...`
- `drift-check` -- checks out both `atlasshare/atlax-tools` and
  `atlasshare/atlax` in sibling directories, then runs
  `go run ./cmd/driftcheck --community-path ../atlax/pkg/config/config.go`

Action versions (`actions/checkout@v6`, `actions/setup-go@v6`) match
the community atlax workflow at `atlax/.github/workflows/ci.yml`.

## Tests added

`cmd/driftcheck/main_test.go`:

- `TestParseStructs_ExtractsNameAndYAMLTag`
- `TestParseStructs_HandlesOmitemptyAndOptions`
- `TestParseStructs_IgnoresFieldsWithoutYAMLTag`
- `TestParseStructs_IgnoresExplicitlySkippedFields` (yaml:"-")
- `TestParseStructs_IgnoresEmbeddedAnonymousFields`
- `TestParseStructs_HandlesMultipleNamesSameType` (grouped declarations)
- `TestCompare_NoDrift`
- `TestCompare_MissingFieldInTools`
- `TestCompare_TagMismatch`
- `TestCompare_ExtraInToolsIsWarning`
- `TestCompare_MissingStructInTools`
- `TestRunDriftCheck_FailsOnParseError`
- `TestRunDriftCheck_RejectsSuspiciousPath`

TDD cycle: RED confirmed before `main.go` was written, then GREEN after
implementation. All tests now pass under `-race`.

## Verification

```
go test -race -count=1 ./cmd/driftcheck/...    -> ok
go vet ./...                                    -> clean
golangci-lint run ./cmd/driftcheck/... \
    ./internal/config/...                       -> 0 issues
go run ./cmd/driftcheck \
    --community-path .../atlax/pkg/config/config.go -> exit 0 (4 warnings)
```

Pre-existing lint issues in unrelated packages (`internal/cli`,
`internal/caddy`, `internal/logger`, `internal/platform`) were not
introduced by this work and are out of scope for G2.

## Commits

1. `f1be7ef` `fix(config): add AdminSocket and ShutdownGracePeriod to RelayServer mirror`
2. `b9bfbef` `feat(driftcheck): add cmd/driftcheck config drift tool`
3. `75d6e6f` `fix(config): close remaining drift detected by driftcheck`
4. `ca1fb93` `style(driftcheck): explicitly discard fmt.Fprintf return values`
5. `0154f6e` `ci: add drift-check job to detect config struct divergence`

Commit 4 is a small cleanup for `errcheck`, kept as its own commit per
repository convention (no amends on committed history).

## Files created

- `cmd/driftcheck/main.go` (~400 lines)
- `cmd/driftcheck/main_test.go` (~250 lines)
- `.github/workflows/ci.yml` (71 lines)
- `docs/step-reports/phase3-step8-g2-report.md` (this file)

## Files modified

- `internal/config/manager.go` (11 new fields, 1 new struct, reorder of ServiceConfig)

## Not pushed, no PR opened

Per task instructions.
