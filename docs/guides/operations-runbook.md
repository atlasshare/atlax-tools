# Operations Runbook

Day-to-day procedures for operating Atlax deployments.

---

## Daily Operations

### Health Check

```bash
ats health
```

Checks:
- Config files readable and valid
- TLS certificates not expired (30-day warning threshold)
- Listeners responding
- Admin/metrics endpoint reachable
- All customer ports listening
- Systemd services active
- mTLS handshake (agent mode)
- Local services reachable (agent mode)

### Log Review

```bash
# Relay logs
journalctl -u atlax-relay --since "24 hours ago" | grep -E "error|warn"

# Agent logs
journalctl -u atlax-agent --since "24 hours ago" | grep -E "error|warn"

# Tool operation log (all actions taken by ats)
cat ~/.ats/logs/$(date +%Y-%m-%d).jsonl | jq .
```

### Metrics

```bash
# Connected agents
curl -s http://localhost:9090/metrics | grep atlax_relay_agents_connected

# Active streams
curl -s http://localhost:9090/metrics | grep atlax_relay_streams_active

# Total streams since start
curl -s http://localhost:9090/metrics | grep atlax_relay_streams_total
```

---

## Customer Management

### Onboarding a New Customer

```bash
# 1. Generate agent cert
ats certs issue
# Enter: customer prefix, customer ID suffix, validity

# 2. Add customer to relay config
ats customer add
# Enter: relay config path, customer details, port allocations

# 3. Restart relay
sudo systemctl restart atlax-relay

# 4. Set up agent on customer's machine
ats setup agent
# Or provide them the cert files and agent.yaml template

# 5. Verify
ats health
```

### Listing Customers

```bash
ats customer list
```

### Adding a Service to an Existing Customer

```bash
ats service add
# Select customer, choose service type, port auto-allocated in 18000-18999 range
```

---

## Service Restarts

### Relay Restart (graceful)

```bash
# Sends GOAWAY to all agents, waits for drain period, then exits
sudo systemctl restart atlax-relay

# Agents auto-reconnect within 5-30 seconds
```

### Agent Restart

```bash
sudo systemctl restart atlax-agent

# Agent reconnects to relay automatically
```

### Full Stack Restart

```bash
# Restart relay first, then agents
sudo systemctl restart atlax-relay
# Wait 10 seconds for relay to be ready
sleep 10
ssh agent-host "sudo systemctl restart atlax-agent"
```

---

## Backup & Restore

### Creating a Backup

```bash
ats backup create
```

Backs up configs + certs to `~/.ats/backups/atlax-backup-<hostname>-<timestamp>.tar.gz`.

Options:
- Include/exclude private keys
- Custom backup directory

### Restoring from Backup

```bash
ats backup restore
# Lists available backups, previews contents, confirms before overwriting
```

### Manual Backup

```bash
tar -czf atlax-backup-$(date +%Y%m%d).tar.gz /etc/atlax/
```

### Scheduled Backups (cron)

```bash
# Daily backup at 2 AM, keep 30 days
0 2 * * * /usr/local/bin/ats backup create --no-color 2>&1 >> /var/log/atlax/backup.log
0 3 * * * find ~/.ats/backups/ -name "*.tar.gz" -mtime +30 -delete
```

---

## Certificate Rotation

### Checking Expiry

```bash
# Check all certs with 30-day threshold
ats certs rotate

# Inspect a specific cert
ats certs inspect /etc/atlax/certs/relay.crt
```

### Rotating Leaf Certs

```bash
# 1. On workstation: regenerate
ats certs rotate
# Auto-detects expiring certs and regenerates them

# 2. Distribute to nodes
scp relay.crt relay.key relay-host:/etc/atlax/certs/
scp agent.crt agent.key agent-host:/etc/atlax/certs/

# 3. Restart
ssh relay-host "sudo systemctl restart atlax-relay"
ssh agent-host "sudo systemctl restart atlax-agent"

# 4. Verify
ats health
```

---

## Configuration Changes

### Modifying Config

When changing relay or agent config:

1. **Backup first:**
   ```bash
   ats backup create
   ```

2. **Edit config:**
   ```bash
   sudo nano /etc/atlax/relay.yaml
   ```

3. **Validate (dry-run restart):**
   ```bash
   atlax-relay -config /etc/atlax/relay.yaml --validate
   # Or just restart — it fails fast on invalid config
   ```

4. **Restart:**
   ```bash
   sudo systemctl restart atlax-relay
   ```

### Using ats for Config Changes

```bash
# Add a service (edits relay.yaml + agent.yaml automatically)
ats service add

# Add a customer (edits relay.yaml, generates certs)
ats customer add
```

Both commands:
- Create a `.bak.yaml` backup before writing
- Validate port conflicts
- Check service name uniqueness
- Let you choose in-place edit vs full regeneration

---

## Uninstalling

### Full Removal

```bash
ats uninstall
```

Interactive prompts for what to remove:
- Binaries
- Configuration files
- Certificates
- Logs
- System user (`atlax`)
- System group (`atlax`)
- Systemd units

Offers to create a backup before removing config/certs.

### Partial Removal

```bash
# Just stop the service without removing files
sudo systemctl stop atlax-relay
sudo systemctl disable atlax-relay
```

---

## Emergency Procedures

### Agent Lost Connection

1. Check relay is running: `systemctl status atlax-relay`
2. Check agent logs: `journalctl -u atlax-agent -f`
3. Agent auto-reconnects — wait 30-60 seconds
4. If not reconnecting: check network, DNS, firewall

### Relay Down

1. Restart: `sudo systemctl restart atlax-relay`
2. All agents auto-reconnect within their backoff window
3. Check logs for crash reason: `journalctl -u atlax-relay --since "10 minutes ago"`

### Compromised Certificate

1. **Immediately** revoke by removing the customer from `relay.yaml`
2. Restart relay: `sudo systemctl restart atlax-relay`
3. Generate new cert: `ats certs issue`
4. Distribute new cert to the agent
5. Re-add customer to relay config
6. Restart both relay and agent

### Full Disaster Recovery

1. Provision new relay: `ats setup relay`
2. Restore backup: `ats backup restore`
3. Update DNS to point to new relay IP
4. Agents reconnect automatically (if relay address is a domain)

---

## Monitoring Integration

### Prometheus

Scrape config:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: atlax-relay
    static_configs:
      - targets: ['relay-host:9090']
    metrics_path: /metrics
```

### Alert Rules

```yaml
# Alert if no agents connected
- alert: AtlaxNoAgents
  expr: atlax_relay_agents_connected == 0
  for: 5m
  labels:
    severity: critical

# Alert if cert expires within 14 days
- alert: AtlaxCertExpiring
  expr: atlax_cert_days_remaining < 14
  labels:
    severity: warning
```

### Uptime Check

```bash
# Simple HTTP health check
curl -sf http://localhost:9090/healthz || echo "RELAY DOWN"

# With uptime monitoring (e.g., UptimeRobot, Healthchecks.io)
# Point to: http://relay-host:9090/healthz
```
