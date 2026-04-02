# Certificate Management Guide

How to generate, distribute, rotate, and troubleshoot Atlax TLS certificates.

---

## PKI Architecture

Atlax uses a two-tier intermediate CA model for mTLS 1.3:

```
Root CA (10-year, RSA-4096, OFFLINE)
├── Relay Intermediate CA (3-year, RSA-4096)
│   └── relay.crt (90-day, RSA-2048)
│       CN: relay.atlax.local
│       SAN: relay.atlax.local, localhost, 127.0.0.1
│       ExtKeyUsage: ServerAuth
│
└── Customer Intermediate CA (3-year, RSA-4096)
    ├── customer-dev-001 (90-day, RSA-2048)
    │   CN: customer-dev-001
    │   ExtKeyUsage: ClientAuth
    │
    ├── customer-prod-abc123 (90-day, RSA-2048)
    │   CN: customer-prod-abc123
    │   ExtKeyUsage: ClientAuth
    │
    └── ... (one cert per customer)
```

**Why two intermediate CAs?**

- **Relay CA** only signs server certs — compromise doesn't affect customer identity
- **Customer CA** only signs client certs — compromise doesn't allow relay impersonation
- **Root CA** stays offline — intermediate CAs can be rotated without disrupting the root trust chain

## Initial PKI Setup

### Using ats

```bash
ats certs init
```

Interactive prompts:
1. **Output directory** — where to store all PKI files
2. **Backend** — OpenSSL (default, always available) or step-ca (if installed)
3. **Relay domain** — CN and primary SAN for the relay cert
4. **Additional SANs** — extra domains/IPs for the relay cert
5. **Initial customer ID** — the first agent cert to issue
6. **Validity periods** — customizable per tier

### Manual with OpenSSL

If you prefer to run OpenSSL directly:

```bash
# 1. Root CA
openssl req -x509 -new -nodes -newkey rsa:4096 \
  -keyout root-ca.key -out root-ca.crt \
  -days 3650 -subj "/C=US/O=Atlax/CN=Atlax Root CA"

# 2. Relay Intermediate CA
openssl req -new -nodes -newkey rsa:4096 \
  -keyout relay-ca.key -out relay-ca.csr \
  -subj "/C=US/O=Atlax/CN=Atlax Relay CA"

openssl x509 -req -in relay-ca.csr \
  -CA root-ca.crt -CAkey root-ca.key -CAcreateserial \
  -out relay-ca.crt -days 1095 \
  -extfile <(echo "[v3_ca]
basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign")

# 3. Customer Intermediate CA
openssl req -new -nodes -newkey rsa:4096 \
  -keyout customer-ca.key -out customer-ca.csr \
  -subj "/C=US/O=Atlax/CN=Atlax Customer CA"

openssl x509 -req -in customer-ca.csr \
  -CA root-ca.crt -CAkey root-ca.key -CAcreateserial \
  -out customer-ca.crt -days 1095 \
  -extfile <(echo "[v3_ca]
basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign")

# 4. Relay leaf cert
openssl req -new -nodes -newkey rsa:2048 \
  -keyout relay.key -out relay.csr \
  -subj "/C=US/O=Atlax/CN=relay.atlax.local"

openssl x509 -req -in relay.csr \
  -CA relay-ca.crt -CAkey relay-ca.key -CAcreateserial \
  -out relay.crt -days 90 \
  -extfile <(echo "[v3_req]
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1")

# 5. Agent leaf cert
openssl req -new -nodes -newkey rsa:2048 \
  -keyout agent.key -out agent.csr \
  -subj "/C=US/O=Atlax/CN=customer-dev-001"

openssl x509 -req -in agent.csr \
  -CA customer-ca.crt -CAkey customer-ca.key -CAcreateserial \
  -out agent.crt -days 90 \
  -extfile <(echo "[v3_req]
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth")
```

## Issuing New Agent Certs

When onboarding a new customer:

```bash
ats certs issue
```

This generates a new agent cert signed by the existing customer CA. The cert CN becomes the customer ID used in the relay config.

**Manual equivalent:**

```bash
openssl req -new -nodes -newkey rsa:2048 \
  -keyout agent-new.key -out agent-new.csr \
  -subj "/C=US/O=Atlax/CN=customer-prod-abc123"

openssl x509 -req -in agent-new.csr \
  -CA customer-ca.crt -CAkey customer-ca.key -CAcreateserial \
  -out agent-new.crt -days 90 \
  -extfile <(echo "[v3_req]
basicConstraints=CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth")
```

## Certificate Distribution

### What goes where

| File | Relay | Agent | Workstation |
|------|:-----:|:-----:|:-----------:|
| `root-ca.crt` | yes | — | yes |
| `root-ca.key` | **NO** | **NO** | OFFLINE |
| `relay-ca.crt` | yes | yes | yes |
| `relay-ca.key` | — | — | yes |
| `customer-ca.crt` | yes | — | yes |
| `customer-ca.key` | — | — | yes |
| `relay.crt` | yes | — | — |
| `relay.key` | yes | — | — |
| `agent.crt` | — | yes | — |
| `agent.key` | — | yes | — |

**Security rules:**
- `root-ca.key` is NEVER on a networked machine after initial setup
- `relay-ca.key` and `customer-ca.key` stay on the workstation that issues certs
- Private keys (`.key`) are chmod `0640`, owned by the `atlax` group (group-readable for the service)
- Transfer certs over SCP, not unencrypted channels

### Transfer commands

```bash
# To relay
scp relay.crt relay.key root-ca.crt customer-ca.crt relay-host:/etc/atlax/certs/

# To agent
scp agent.crt agent.key relay-ca.crt agent-host:/etc/atlax/certs/
```

## Certificate Rotation

### Checking expiry

```bash
# Interactive check with auto-rotation option
ats certs rotate

# Quick inspection of a single cert
ats certs inspect /etc/atlax/certs/relay.crt
```

### Rotation workflow

**Leaf certs (relay.crt, agent.crt) — every 90 days:**

1. Run `ats certs rotate` on your workstation
2. It scans all certs, identifies expiring ones
3. Regenerates leaf certs using existing intermediate CAs
4. Creates timestamped backups of old certs
5. You distribute new certs and restart services

```bash
# After rotation:
scp relay.crt relay.key relay-host:/etc/atlax/certs/
ssh relay-host "sudo systemctl restart atlax-relay"

scp agent.crt agent.key agent-host:/etc/atlax/certs/
ssh agent-host "sudo systemctl restart atlax-agent"
```

**Intermediate CAs (relay-ca.crt, customer-ca.crt) — every 3 years:**

1. Generate new intermediate CAs signed by the root CA
2. Re-issue all leaf certs under the new intermediates
3. Distribute to all nodes
4. Rolling restart (agents auto-reconnect)

**Root CA (root-ca.crt) — every 10 years:**

1. Generate a new root CA
2. Regenerate the entire PKI hierarchy
3. Distribute to all nodes simultaneously
4. This is a rare, planned maintenance event

### Automation with cron

```bash
# Check cert expiry weekly, alert if any expire within 30 days
0 9 * * 1 /usr/local/bin/ats certs rotate --dry-run 2>&1 | \
  grep -E "(EXPIRED|expires in)" && \
  echo "Atlax certs need rotation" | mail -s "Cert Alert" ops@example.com
```

## Inspecting Certificates

```bash
# Using ats
ats certs inspect /etc/atlax/certs/relay.crt

# Output:
#   Subject      CN=relay.atlax.local,O=Atlax,C=US
#   Issuer       CN=Atlax Relay CA,O=Atlax,C=US
#   Not Before   2026-04-01 00:00:00 UTC
#   Not After    2026-06-30 00:00:00 UTC
#   Days Left    90
#   Is CA        false
#   DNS SANs     [relay.atlax.local localhost]
#   IP SANs      [127.0.0.1]
#   Extended Key Usage  [ServerAuth]

# Using OpenSSL directly
openssl x509 -in /etc/atlax/certs/relay.crt -text -noout

# Verify chain
openssl verify -CAfile root-ca.crt -untrusted relay-ca.crt relay.crt
```

## Troubleshooting

| Error | Cause | Fix |
|-------|-------|-----|
| `certificate has expired` | Leaf cert past NotAfter | Rotate with `ats certs rotate` |
| `unknown authority` | Agent cert not signed by customer-ca | Verify cert chain: `openssl verify -CAfile customer-ca.crt agent.crt` |
| `bad certificate` | Wrong cert type (server vs client) | Relay needs ServerAuth, agent needs ClientAuth |
| `certificate signed by unknown authority` | Missing intermediate CA | Ensure relay has `customer-ca.crt` in `client_ca_file` |
| `remote error: tls: bad certificate` | Key doesn't match cert | Re-issue cert with matching key pair |
| `x509: certificate is valid for X, not Y` | SAN mismatch | Regenerate relay cert with correct domain in SAN |
