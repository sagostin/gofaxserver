# Customer Setup Guide (Tenant Onboarding)

This document describes the procedure for adding a new customer/tenant to gofaxserver.

> **Prerequisite:** a running gofaxserver + FreeSWITCH stack. If you haven't
> installed the system yet, start with [INSTALLATION.md](INSTALLATION.md) and
> come back here for day-2 customer onboarding.

## Overview

The fax relay system consists of:
- **FreeSWITCH** (`examples/freeswitch/`) — SIP gateway and T.38 termination. Listens for inbound ESL on `:8022`.
- **gofaxserver** (`gofaxserver/server.go`, entry point `gofaxserver/cmd/gofaxserver/main.go`) — Multi-tenant fax routing and management.
- **PostgreSQL** — Persistent storage for tenants, numbers, endpoints, tenant users, and fax job results. Schema is auto-migrated on first startup.
- **REST API** — Administration interface on `:8080` (or `web.listen` from `config.json`).

## Supported Integration Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| **Relay** | Passes T.38 through without transcoding | Same T.38 support on both ends |
| **Transcoding/Bridge** | Converts between T.38 and G.711 | Different fax capabilities on each end (recommended for new customers) |

See [TENANTS.md](TENANTS.md) for detailed configuration differences.

---

## Step 1: Connect to the Fax Server

```bash
ssh <ADMIN_USER>@<FAX_SERVER_HOST>
sudo su
```

---

## Step 2: Configure the FreeSWITCH Gateway

> **Automated alternative:** if `freeswitch.gateway_config_dir` is set in
> gofaxserver's `config.json`, gateways can be provisioned end-to-end from the
> portal (**Admin → Gateways**) or the API (`POST /admin/gateway`) — template
> render, XML write, `sofia profile fax rescan`, and endpoint creation in one
> call. See "API-driven provisioning" in [GATEWAYS.md](GATEWAYS.md). The
> manual flow below still works and is the fallback when provisioning is
> disabled or gofaxserver and FreeSWITCH share no filesystem.
>
> **Prerequisites for automated provisioning (FreeSWITCH as host service):**
> 1. The sofia profile must include the gateways directory. Our example config
>    does this (`autoload_configs/sofia.conf.xml`):
>    `<X-PRE-PROCESS cmd="include" data="../gateways/*.xml"/>` inside
>    `<profile><gateways>`. If gateways provisioned via the API show as
>    `not-loaded`, this include is missing.
> 2. Both processes must be able to write/read the directory. Recommended:
>    ```bash
>    chgrp freeswitch /etc/freeswitch/gateways
>    chmod g+ws /etc/freeswitch/gateways   # setgid: new files inherit the group
>    usermod -aG freeswitch gofax          # gofaxserver's user joins the group
>    ```
>    Rendered files are written `0640`. If ownership must be exact, set
>    `freeswitch.gateway_config_chown` (e.g. `"freeswitch:freeswitch"`) and run
>    gofaxserver with permission to chown (root or `CAP_CHOWN`); chown failures
>    are logged as warnings, not fatal.

Templates are in `examples/freeswitch/gateways/`. Copy the appropriate one for this customer:

```bash
# Customer PBX gateway
cp examples/freeswitch/gateways/pbx_example.xml /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml

# Upstream carrier / SBC gateway (if this is for an upstream trunk)
cp examples/freeswitch/gateways/sbc_example.xml /etc/freeswitch/gateways/sbc_<CARRIERNAME>.xml
```

Edit the gateway file and update:

```xml
<include>
    <gateway name="pbx_<CUSTOMERNAME>">
        <param name="realm" value="<CUSTOMER_PBX_HOST_OR_IP>"/>
        <param name="extension" value="auto_to_user"/>
        <param name="caller-id-in-from" value="true"/>
        <param name="extension-in-contact" value="false"/>
        <param name="register" value="false"/>
    </gateway>
</include>
```

**Key gateway parameters:**
- `realm` — The public host or domain of the remote peer. **This is also matched by gofaxserver's `fsGatewayACL`** (the endpoint's `endpoint` value must be `xml_name:publicIP`, and the part after the `:` is compared exactly against the inbound source IP) for inbound ACL to pass.
- `extension` — How to route calls to this gateway (default: `auto_to_user`).
- `register` — Set to `false` for IP-based authentication.

> **Tip:** For upstream/SBC gateways, prefer the `sbc_example.xml` template — it sets `extension-in-contact=true` and `ignore-early-media=true`, both of which the routing code relies on.

---

## Step 3: Configure the Customer PBX

On the customer's PBX:

1. **Create SIP Trunk**
   - Host: `<FAX_SERVER_HOST>`
   - Specify Original Calling Party

2. **Configure Dial Plans**
   - Rule to match MX Fax Service for outgoing fax relay
   - Rule to match internal extension for physical fax machine (FXS gateway)

3. **Inbound Configuration**
   - Assign a Fax DID number, **or**
   - Add user to a Fax group

---

## Step 4: Access FreeSWITCH CLI

```bash
fs_cli
```

Exit with `/quit`.

**Useful CLI commands:**

```bash
# Disable SIP trace (reduce noise)
sofia global siptrace off

# Reload gateway configurations on the 'fax' profile
sofia profile fax rescan

# Check gateway status
sofia status gateway pbx_<CUSTOMERNAME>
```

The `fax` profile name comes from `examples/freeswitch/autoload_configs/sofia.conf.xml` and points its dialplan at `socket:127.0.0.1:8022 async full` — the same port gofaxserver listens on for inbound ESL.

---

## Step 5: Reload Gateway in FreeSWITCH

> Not needed when the gateway was provisioned via the portal/API — the reload
> (`reloadxml` + `sofia profile fax rescan`) happens automatically.

After creating the gateway XML file, scan it in the FreeSWITCH CLI:

```bash
sofia profile fax rescan
```

You should see the newly scanned gateway listed.

---

## Step 6: Create Tenant via API

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/tenant \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "<TENANT_NAME>",
    "notify": ""
  }'
```

**Response includes an ID** — save this as `<TENANT_ID>` for subsequent steps.

### About the `notify` Field

The notify field specifies destinations for **fax completion notifications**. Multiple destinations can be combined, separated by commas. The parser (`gofaxserver/notify.go:parseNotifyString`) splits first on commas, then on `->`, so each segment must be `<type>-><destination>`.

**Supported types:**
| Type | Description |
|------|-------------|
| `email_report-><email>` | Email with PDF report only |
| `email_full-><email>` | Email with PDF report + original fax attachment |
| `email_full_failure-><email>` | Like `email_full` but only sent on failed faxes |
| `webhook-><URL>` | HTTP POST with JSON payload (base64 PDF report + fax job data) |
| `webhook_form-><URL>` | Multipart form POST with first page PDF attached (for n8n) |

**Example:**
```
email_full_failure->support@customer.com,webhook_form->https://n8n.example.com/webhook/123
```

For transcoded/bridged calls, this field is generally **not used** (the source/dest tenants each generate their own notifications).

Multiple email addresses for the same notification type are separated by `;` — only valid for `email_*` types (the SMTP layer splits on `;`).

---

## Step 7: Add Endpoint

Endpoint format for gateways: `<GATEWAY_XML_NAME>:<PBX_IP>` (the `publicIP` is what gofaxserver's `fsGatewayACL` checks against the inbound source IP).

### Endpoint Types

| Type | Description | Endpoint Format | `type_id` |
|------|-------------|------------------|-----------|
| `gateway` | SIP gateway to PBX | `xml_name:ip` | per scope |
| `webhook` | HTTP POST delivery | Full URL (e.g., `https://...`) | per scope |
| `email` | Email delivery | Email address | per scope |

**Endpoint scope** (set by `type`):

| `type` | Description | `type_id` |
|--------|-------------|-----------|
| `tenant` | Applies to all numbers in the tenant | Tenant ID |
| `number` | Applies to a specific number only (overrides tenant) | TenantNumber ID (DB primary key) |
| `global` | Upstream carrier gateway; fallback for all tenants | `0` |

### Gateway Endpoint (Relay Mode)

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "gateway",
    "endpoint": "pbx_<CUSTOMERNAME>:<CUSTOMER_PBX_IP>",
    "priority": 0
  }'
```

### Gateway Endpoint (Transcoding/Bridge Mode — Recommended for New Customers)

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "gateway",
    "endpoint": "pbx_<CUSTOMERNAME>:<CUSTOMER_PBX_IP>",
    "priority": 0,
    "bridge": true
  }'
```

The `bridge: true` flag enables T.38/G.711 transcoding for this endpoint.

### Webhook Endpoint

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "webhook",
    "endpoint": "https://your-webhook-server.com/fax/receive",
    "priority": 0
  }'
```

### Email Endpoint

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "email",
    "endpoint": "fax@destination.com",
    "priority": 0
  }'
```

### Global Upstream Endpoint

Upstream carrier gateways are registered as `type=global, type_id=0`. They are loaded into `UpstreamFsGateways` and used as a fan-out fallback when no tenant/number endpoint matches.

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "global",
    "type_id": 0,
    "endpoint_type": "gateway",
    "endpoint": "sbc_carrier1:198.51.100.10",
    "priority": 0
  }'
```

### Priority

| Priority Value | Behavior |
|----------------|----------|
| `0` | Highest priority (primary endpoint) |
| `1+` | Lower priority (failover endpoints) |
| `666` | Endpoint is **filtered out for inbound delivery** but still usable as a source gateway for outbound bridge calls |
| `999` | Reserved for the implicit upstream-fallback group |

**How priority works:**
- Lower numbers = higher preference
- If the primary endpoint (priority 0) fails, the system attempts the next highest priority
- Priority 666 lets you mark an endpoint as "outbound-only" for bridge scenarios

**Example — Gateway with Failover:**

```bash
# Primary gateway
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "gateway",
    "endpoint": "pbx_primary:192.168.1.10",
    "priority": 0
  }'

# Failover gateway
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "gateway",
    "endpoint": "pbx_secondary:192.168.1.11",
    "priority": 1
  }'
```

---

## Step 8: Add Phone Numbers

```bash
curl -X POST http://<FAX_SERVER_HOST>:8080/admin/number \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": <TENANT_ID>,
    "number": "<10_DIGIT_NUMBER>",
    "name": "<DISPLAY_NAME>",
    "header": "<FAX_HEADER>",
    "notify": "email_full_failure->failures@customer.com"
  }'
```

Add each number with a separate command. The optional `notify` field overrides the tenant-level `notify` for faxes involving this number.

---

## Step 9: Reload Configuration

```bash
curl -X GET http://<FAX_SERVER_HOST>:8080/admin/reload \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)"
```

This hot-reloads tenants, numbers, and endpoints from the database into memory.

---

## Step 10: Configure SBC Digitmap (If Applicable)

If using a Session Border Controller (third-party — not part of gofaxserver):

```bash
# Remove old numbers from previous NAP
delete-tbcustomernumbers -numbers <NUMBER1>,<NUMBER2>

# Add numbers to fax relay
add-tbcustomernumbers -customer FaxServer1 -numbers <NUMBER1>,<NUMBER2> -port

# Activate configuration
activate-tbcustomerconfig
```

---

## Step 11: Test Fax Functionality

1. Send a test fax **inbound** (external → PBX → fax relay)
2. Send a test fax **outbound** (fax relay → PBX → external)
3. Verify logs appear in stdout / Loki / Grafana

---

## Step 12: Monitoring

- Active fax jobs: `curl http://<FAX_SERVER_HOST>:8080/admin/faxes` (admin auth)
- Fax status by UUID: `curl "http://<FAX_SERVER_HOST>:8080/fax/status?uuid=<JOB_UUID>"` (tenant user auth, not admin)
- FreeSWITCH debugging: `fs_cli` then `sofia global siptrace on`
- Loki queries: filter by `job="faxserver"` and the component type (`Server.StartUp`, `Router`, `Queue`, etc.)

---

## Dialplan Transformations

Caller/callee numbers are normalized before any tenant lookup. Rules are configured in the optional `dialplan` section of `config.json`:

```json
"dialplan": {
  "rules": [
    { "pattern": "^1(\\d{10}).*$", "replacement": "$1" },
    { "pattern": "^(\\d{10}).*$", "replacement": "$1" }
  ]
}
```

Each rule is a regex pattern and a replacement string (supports `$1`, `$2`, ...). Rules are applied in order to both caller and callee numbers (`gofaxserver/server.go:loadDialplan`).

- **Section absent** (default): the built-in NANP rules are used — strip a leading `1` from 11-digit numbers, then truncate to 10 digits. This matches tenant numbers stored in 10-digit format.
- **Section present**: the configured rules fully replace the defaults. An empty list (`"rules": []`) disables transformation entirely — recommended for non-NANP (international) deployments, since the default 10-digit truncation corrupts longer numbers.
- An invalid pattern logs an error at startup and falls back to the default rules.

**Database-backed mode:** set `"source": "db"` in the `dialplan` section to manage rules in the `dialplan_rules` table instead — hot-reloadable without restart via the portal (**Admin → Dialplan**), the API (`/admin/dialplan/rules`), or `/admin/reload`. On first run the table is seeded from your config rules (or the defaults), so switching modes preserves behavior. Rules have a position (order), an enabled flag, and a description; invalid patterns are rejected on write.

---

## Python TUI (Optional)

For an interactive terminal-based setup wizard:

```bash
pip install prompt_toolkit requests

export FAX_API_URL=http://<FAX_SERVER>:8080
export FAX_API_KEY=<your_api_key>

python scripts/setup-tenant-tui.py
```

The TUI provides a step-by-step wizard for creating tenants, endpoints, and numbers. FreeSWITCH gateway configuration must still be done manually.

---

## Related Documentation

- [INSTALLATION.md](INSTALLATION.md) — Full installation guide (prerequisite)
- [TENANTS.md](TENANTS.md) — Detailed tenant modes and configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
- [API_REFERENCE.md](API_REFERENCE.md) — Full API documentation
- [GATEWAYS.md](GATEWAYS.md) — FreeSWITCH gateway configuration details
- [PORTAL.md](PORTAL.md) — Web portal for end-user faxing (automates most of this setup)
