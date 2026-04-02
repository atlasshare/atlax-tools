# Quick Start Guide

Get an Atlax relay + agent pair running in under 15 minutes.

---

## Prerequisites

| Requirement | Why |
|-------------|-----|
| Relay host | A machine with a public IP (VPS, cloud instance, bare metal) |
| Agent host | A machine behind NAT/CGNAT (home server, office node, edge device) |
| OpenSSL **or** step CLI | Certificate generation |
| Go 1.25+ (optional) | Only needed if building from source |

## 1. Install the Tool

Download the binary for your platform from GitHub Releases:

```bash
# Linux (amd64)
curl -fsSL -o ats \
  https://github.com/atlasshare/atlax/releases/latest/download/ats-linux-amd64
chmod +x ats
sudo mv ats /usr/local/bin/

# macOS (Apple Silicon)
curl -fsSL -o ats \
  https://github.com/atlasshare/atlax/releases/latest/download/ats-darwin-arm64
chmod +x ats
sudo mv ats /usr/local/bin/
```

Or build from source:

```bash
cd ~/projects/tooling-scripts/atlax-tools
make build
sudo mv bin/ats /usr/local/bin/
```

Verify:

```bash
ats --version
```

## 2. Generate Certificates

On your workstation (the machine with secure storage for the root CA key):

```bash
ats certs init
```

The tool will interactively ask for:
- **Output directory** — where to store the PKI files (default: `./certs`)
- **Certificate backend** — OpenSSL or step-ca
- **Relay domain** — the CN/SAN for the relay cert (e.g., `relay.atlax.local`)
- **Customer ID** — the initial agent identity (e.g., `customer-dev-001`)
- **Validity periods** — Root CA (10yr), intermediates (3yr), leaf certs (90d)

Output:

```
certs/
├── root-ca.crt          # Root CA (distribute to all nodes)
├── root-ca.key          # ROOT CA KEY — store OFFLINE
├── relay-ca.crt         # Relay intermediate CA
├── relay-ca.key         # Signs relay certs
├── customer-ca.crt      # Customer intermediate CA
├── customer-ca.key      # Signs agent certs
├── relay.crt            # Relay leaf cert
├── relay.key            # Relay private key
├── agent.crt            # Agent leaf cert (CN=customer-dev-001)
├── agent.key            # Agent private key
├── relay-chain.crt      # relay.crt + relay-ca.crt
├── agent-chain.crt      # agent.crt + customer-ca.crt
└── intermediate-cas.crt # Both intermediate CAs bundled
```

## 3. Provision the Relay

SSH into your relay host and run:

```bash
ats setup relay
```

The wizard walks through configuration, then executes with a visual checklist showing progress:

1. **Binary source** — download from GitHub, build from source, or specify a local path
2. **Config directory** — where to store `relay.yaml` and certs (default: `/etc/atlax/`)
3. **Server settings** — listener address, admin port, max agents/streams
4. **TLS** — relay domain, SANs, cert generation or copy
5. **Initial customer** — ID, port allocations, service types
6. **Firewall** — auto-configure UFW/firewalld, print AWS Security Group commands
7. **System service** — create `atlax` group (controls file access), optional `atlax` user (runs daemon), install systemd unit

If the setup is interrupted (SSH drops, network issue), re-running `ats setup relay` detects the checkpoint and offers to resume from the last completed step.

After completion:

```bash
# Verify the relay is running
systemctl status atlax-relay

# Check health
curl http://localhost:9090/healthz
```

## 4. Provision the Agent

SSH into your agent host and run:

```bash
ats setup agent
```

The wizard walks through:

1. **Binary source** — same options as relay
2. **Config directory** — default: `/etc/atlax/`
3. **Relay connection** — relay address (e.g., `relay.example.com:8443`), server name
4. **Services** — register local services the agent will expose (e.g., `http → 127.0.0.1:3000`)
5. **Certificates** — copy from a local path or provide manually
6. **System service** — systemd unit installation

After completion:

```bash
# Verify the agent connects
systemctl status atlax-agent

# Check the relay logs for the connection
journalctl -u atlax-relay --since "2 minutes ago" | grep "agent connected"
```

## 5. Verify End-to-End

From outside (or the relay itself):

```bash
# If you allocated port 18080 for the http service:
curl http://localhost:18080
```

If you get a response from your local service, the tunnel is working.

## 6. Add HTTPS (Optional)

If you have a domain and Caddy installed on the relay:

```bash
ats service add
# When prompted:
#   - Select your customer
#   - Choose service type: http
#   - Accept the auto-allocated port
#   - Say "yes" to adding a Caddy block
#   - Enter your domain (e.g., app.example.com)
```

The tool appends a reverse proxy block to your Caddyfile. Reload Caddy and you have auto-HTTPS:

```bash
sudo systemctl reload caddy
curl https://app.example.com
```

---

## What's Next

| Task | Command |
|------|---------|
| Add another customer | `ats customer add` |
| Add a service to an existing customer | `ats service add` |
| Check certificate expiry | `ats certs rotate` |
| Run diagnostics | `ats health` |
| Back up configs + certs | `ats backup create` |
| View a cert's details | `ats certs inspect ./certs/relay.crt` |
