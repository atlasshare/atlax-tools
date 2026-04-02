# CLI Reference

Complete reference for all `ats` commands, flags, and options.

---

## Global Flags

Available on every command:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dry-run` | bool | `false` | Show planned actions without executing |
| `--no-color` | bool | `false` | Disable ANSI colored output |
| `-v, --verbose` | bool | `false` | Show platform detection details and step progress |
| `--log-dir` | string | `~/.ats/logs/` | Custom directory for operation logs |
| `-h, --help` | — | — | Help for any command |
| `--version` | — | — | Print version and exit |

---

## `ats setup relay`

Interactively provision a relay node on the current machine.

```bash
ats setup relay [flags]
```

**Interactive prompts:**

| Step | Prompt | Default |
|------|--------|---------|
| 1 | Binary source (download/build/local) | Download |
| 2 | Config directory | `/etc/atlax` |
| 2 | Certificate directory | `/etc/atlax/certs` |
| 3 | Agent listener address | `0.0.0.0:8443` |
| 3 | Admin/metrics address | `127.0.0.1:9090` |
| 3 | Max concurrent agents | `100` |
| 3 | Max streams per agent | `100` |
| 4 | Relay domain (cert CN/SAN) | `relay.atlax.local` |
| 4 | Additional SANs | `localhost,127.0.0.1` |
| 4 | Generate certificates? | Yes |
| 4 | Certificate backend | Auto-detected |
| 5 | Add initial customer? | Yes |
| 5 | Customer ID prefix | `customer` |
| 5 | Customer ID suffix | `dev-001` |
| 5 | Port allocations | Interactive per-port |
| 6 | Configure firewall? | Yes |
| 6 | Print AWS SG commands? | No |
| 7 | Install as system service? | Yes |
| 7 | Create 'atlax' system group? | Yes |
| 7 | Create dedicated 'atlax' service user? | Yes |

**What it does:**

Each step is tracked in a visual checklist with checkpoint persistence at `~/.ats/checkpoints/setup-relay.json`. If the setup is interrupted, re-running the command detects the checkpoint and offers to resume from the last completed step.

1. Creates `/etc/atlax/` and `/etc/atlax/certs/`
2. Installs binary to `/usr/local/bin/atlax-relay`
3. Generates full PKI (if requested)
4. Writes `relay.yaml` with all settings
5. Configures firewall (UFW/firewalld/iptables)
6. Creates `atlax` group/user and starts systemd service

**Examples:**

```bash
# Full interactive setup
ats setup relay

# Preview without executing
ats setup relay --dry-run

# Verbose output with platform details
ats setup relay -v
```

---

## `ats setup agent`

Interactively provision an agent node on the current machine.

```bash
ats setup agent [flags]
```

**Interactive prompts:**

| Step | Prompt | Default |
|------|--------|---------|
| 1 | Binary source | Download |
| 2 | Config directory | `/etc/atlax` |
| 2 | Certificate directory | `/etc/atlax/certs` |
| 3 | Relay address (host:port) | Required |
| 3 | Relay server name | `relay.atlax.local` |
| 3 | Keepalive interval | `30s` |
| 3 | Keepalive timeout | `10s` |
| 4 | Services (name, local_addr, protocol) | Interactive loop |
| 5 | Copy certs from local path? | Yes |
| 6 | Install as system service? | Yes |

**What it does:**

Each step is tracked in a visual checklist with checkpoint persistence at `~/.ats/checkpoints/setup-agent.json`. If interrupted, re-running resumes from the last completed step.

1. Creates config and cert directories
2. Installs binary
3. Copies certificates from source
4. Writes `agent.yaml`
5. Creates `atlax` group/user and starts systemd service

---

## `ats certs init`

Generate the complete PKI hierarchy from scratch.

```bash
ats certs init [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Output directory | `./certs` |
| Certificate backend | Auto-detected (OpenSSL or step-ca) |
| Relay domain | `relay.atlax.local` |
| Additional SANs | `localhost,127.0.0.1` |
| Initial customer ID | `customer-dev-001` |
| Root CA validity (days) | `3650` |
| Intermediate CA validity (days) | `1095` |
| Leaf cert validity (days) | `90` |

**Output files:**
- `root-ca.crt`, `root-ca.key`
- `relay-ca.crt`, `relay-ca.key`
- `customer-ca.crt`, `customer-ca.key`
- `relay.crt`, `relay.key`
- `agent.crt`, `agent.key`
- `relay-chain.crt`, `agent-chain.crt`, `intermediate-cas.crt`

---

## `ats certs issue`

Issue a new agent certificate for a customer.

```bash
ats certs issue [flags]
```

**Requires:** Existing `customer-ca.crt` and `customer-ca.key` in the cert directory.

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Certificate directory | `./certs` |
| Customer ID prefix | `customer` |
| Customer ID suffix | Required |
| Validity (days) | `90` |
| Backend | Auto-detected |
| Output directory | `./certs/<customer-id>` |

---

## `ats certs rotate`

Scan certificates for expiry and regenerate those within the threshold.

```bash
ats certs rotate [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Certificate directory | `/etc/atlax/certs` |
| Renewal threshold (days) | `30` |

**Behavior:**
- Scans: `relay.crt`, `agent.crt`, `relay-ca.crt`, `customer-ca.crt`, `root-ca.crt`
- For each cert: shows CN, expiry date, days remaining
- Status: EXPIRED, WARNING (within threshold), or OK
- Offers to regenerate expiring leaf certs
- Creates timestamped backups before regenerating
- CA certs flagged for manual renewal (requires root CA key)

---

## `ats certs inspect`

Display detailed information about a certificate file.

```bash
ats certs inspect [cert-path] [flags]
```

**Output fields:** Subject, Issuer, Serial, Not Before, Not After, Days Left, Is CA, Key Usage, DNS SANs, IP SANs, Extended Key Usage.

---

## `ats customer add`

Add a new customer to the relay configuration.

```bash
ats customer add [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Relay config path | `/etc/atlax/relay.yaml` |
| Config update strategy | In-place edit |
| Customer ID prefix | `customer` |
| Customer ID suffix | Required |
| Max agent connections | `1` |
| Max concurrent streams | `100` |
| Port allocations | Interactive loop (18000-18999) |
| Generate agent cert? | Yes |

**Validations:**
- Customer ID must be unique
- Port numbers must not conflict with any existing customer
- Service names must be unique within the customer
- Port range enforced: 18000-18999

**Side effects:**
- Creates `.bak.yaml` backup of the relay config
- Optionally generates agent certificate

---

## `ats customer list`

Display all customers and their port allocations.

```bash
ats customer list [flags]
```

**Output format:**

```
customer-dev-001 (max_conn: 1, max_streams: 100)
    :18070  →  api           listen: 127.0.0.1    Dashboard API
    :18080  →  http          listen: 127.0.0.1    Dashboard web
    :18090  →  portfolio     listen: 127.0.0.1    Portfolio site
──────────────────────────────────────────────────────
```

---

## `ats service add`

Add a new service to both relay and agent configurations.

```bash
ats service add [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Relay config path | `/etc/atlax/relay.yaml` |
| Customer | Select from list |
| Service type | Select from supported list |
| Relay port | Auto-allocated (next free in 18000-18999) |
| Relay listen address | `127.0.0.1` |
| Update agent config? | Yes |
| Agent config path | `/etc/atlax/agent.yaml` |
| Local service address | Required (e.g., `127.0.0.1:3000`) |
| Add Caddy block? | Yes (for HTTP services) |
| Domain | Required (if Caddy) |

**Supported service types:**
`http`, `https`, `tcp`, `smb`, `ssh`, `ftp`, `mysql`, `postgres`, `redis`, `mongodb`, `api`, `grpc`, `websocket`, `custom`

---

## `ats health`

Run comprehensive diagnostics on an Atlax deployment.

```bash
ats health [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Node type | Relay / Agent / Both |
| Config path(s) | Platform default |

**Relay checks:**
- Config file readable and valid
- TLS cert files exist
- Cert not expired (30-day threshold)
- Agent listener responding
- Admin/metrics endpoint reachable (HTTP 200)
- Each customer port listening
- Localhost-only ports not externally reachable
- systemd service active

**Agent checks:**
- Config file readable and valid
- TLS cert files exist
- Cert not expired
- Relay reachable (TCP connect)
- mTLS handshake successful (TLS 1.3 verified)
- Each local service reachable
- systemd service active

**Output:** Pass/Warn/Fail counts with per-check detail.

---

## `ats backup create`

Create a tar.gz archive of configs and certificates.

```bash
ats backup create [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Config directory | `/etc/atlax` |
| Backup directory | `~/.ats/backups/` |
| Include private keys? | Yes |

**Output:** `~/.ats/backups/atlax-backup-<hostname>-<timestamp>.tar.gz` (set to `0444` after creation).

---

## `ats backup restore`

Restore configs and certs from a backup archive.

```bash
ats backup restore [archive-path] [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| Backup archive | Select from available / enter path |
| Restore destination | `/etc/atlax` |

**Safety features:**
- Previews archive contents before extracting
- Requires explicit confirmation
- Path traversal protection (rejects `../` in archive paths)

---

## `ats uninstall`

Remove Atlax relay and/or agent from the current machine.

```bash
ats uninstall [flags]
```

**Interactive prompts:**

| Prompt | Default |
|--------|---------|
| What to uninstall | Relay / Agent / Both |
| Remove binaries? | Yes |
| Remove config files? | No |
| Remove certificates? | No |
| Remove logs? | No |
| Remove 'atlax' user? | No |
| Remove 'atlax' group? | No |
| Create backup first? | Yes (if removing config/certs) |

**Actions:**
1. Stops and disables systemd services
2. Removes service unit files
3. Removes binaries from `/usr/local/bin/`
4. Optionally removes config, certs, logs, system user, system group

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error (config read failure, validation, etc.) |
| `1` | Health check failures (N check(s) failed) |

---

## Log Format

Operation logs are stored as JSONL (one JSON object per line) at `~/.ats/logs/YYYY-MM-DD.jsonl`.

```json
{
  "timestamp": "2026-04-01T16:30:00Z",
  "level": "INFO",
  "command": "service-add",
  "action": "add-port",
  "detail": "customer-dev-001:18090→portfolio",
  "dry_run": false,
  "host": "relay-vps"
}
```

Log files are set to `0444` (read-only) after each write for audit integrity.

---

## Environment Variables

The tool respects:

| Variable | Effect |
|----------|--------|
| `NO_COLOR` | Disables colored output (same as `--no-color`) |
| `HOME` | Used for default log/backup paths |

---

## Shell Completion

```bash
# Bash
ats completion bash > /etc/bash_completion.d/ats

# Zsh
ats completion zsh > "${fpath[1]}/_ats"

# Fish
ats completion fish > ~/.config/fish/completions/ats.fish

# PowerShell
ats completion powershell > ats.ps1
```
