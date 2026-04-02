# ats

Interactive CLI for deploying, configuring, and maintaining Atlax relay and agent nodes.

## Install

```bash
# Download (Linux amd64)
curl -fsSL -o ats \
  https://github.com/atlasshare/atlax/releases/latest/download/ats-linux-amd64
chmod +x ats && sudo mv ats /usr/local/bin/

# Or build from source
make build
```

## Commands

| Command | Purpose |
|---------|---------|
| `setup relay` | Provision a relay node (binary, config, certs, firewall, systemd) |
| `setup agent` | Provision an agent node |
| `certs init` | Generate full PKI (Root CA → Intermediate CAs → leaf certs) |
| `certs issue` | Issue a new agent certificate |
| `certs rotate` | Check expiry and rotate certificates |
| `certs inspect` | Display certificate details |
| `customer add` | Add a customer to relay config |
| `customer list` | List customers and port allocations |
| `service add` | Add a service (updates relay + agent + Caddy) |
| `health` | Run deployment diagnostics |
| `backup create` | Archive configs and certs |
| `backup restore` | Restore from backup |
| `uninstall` | Remove Atlax from a machine |

## Global Flags

```
--dry-run      Show what would happen without executing
--no-color     Disable colored output
-v, --verbose  Show platform details and step progress
--log-dir      Custom log directory (default: ~/.ats/logs/)
```

## Features

- **Visual checklist** with checkpoint persistence — interrupted setups resume from the last completed step
- **Group-first permissions** — `atlax` group controls file access, optional `atlax` user runs the daemon
- **Dual cert backend** — OpenSSL and step-ca with auto-detection
- **Dry-run mode** — preview every action without executing
- **Structured audit log** — JSONL at `~/.ats/logs/`, set to read-only after write

## Platforms

Linux (Ubuntu, Debian, RHEL, Arch, Alpine), macOS, FreeBSD, Windows.

Auto-detects OS, package manager, init system, and firewall tool.

## Documentation

- [Quick Start](docs/guides/quick-start.md) — 15-minute end-to-end setup
- [Relay Deployment](docs/guides/relay-deployment.md) — Full relay guide for all platforms
- [Agent Deployment](docs/guides/agent-deployment.md) — Full agent guide with Docker patterns
- [Certificate Management](docs/guides/certificate-management.md) — PKI, rotation, troubleshooting
- [Operations Runbook](docs/guides/operations-runbook.md) — Day-to-day procedures, backup, monitoring
- [CLI Reference](docs/reference/cli-reference.md) — All commands, flags, and options

## Cross-Compile

```bash
make cross
# Produces: bin/ats-{linux,darwin,freebsd}-{amd64,arm64}, windows-amd64.exe
```

## License

Internal tooling — Atlax project.
