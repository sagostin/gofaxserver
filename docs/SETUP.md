# Customer Setup Guide

This document describes the procedure for adding a new customer/tenant to the Fax Relay system.

## Overview

The fax relay system consists of multiple components:
- **FreeSWITCH** (`gofaxserver/examples/freeswitch/`) - SIP gateway and T.38 termination
- **gofaxserver** (`gofaxserver/server.go`) - Multi-tenant fax routing and management
- **PostgreSQL** - Persistent storage for tenants, numbers, and endpoints
- **REST API** - Administration interface on port 8080

## Supported Integration Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| **Relay** | Passes T.38 through without transcoding | Same T.38 support on both ends |
| **Transcoding/Bridge** | Converts between T.38 and G.711 | Different fax capabilities on each end (recommended for new customers) |

See [TENANTS.md](TENANTS.md) for detailed configuration differences.

---

## Step 1: Connect to the Fax Relay Server

```bash
ssh <ADMIN_USER>@<FAX_SERVER_HOST>
sudo su
```

---

## Step 2: Configure the FreeSWITCH Gateway

Copy the template gateway configuration for the new customer:

```bash
cp /etc/freeswitch/gateways/pbx_example.xml /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml
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
- `realm` - The public host or domain of the customer's PBX
- `extension` - How to route calls to this gateway (default: `auto_to_user`)
- `register` - Set to `false` for IP-based authentication

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
   - Assign a Fax DID number, OR
   - Add user to a Fax group

---

## Step 4: Access FreeSwitch CLI

```bash
fs_cli
```

Exit with `/quit`.

**Useful CLI commands:**

```bash
# Disable SIP trace (reduce noise)
sofia global siptrace off

# Reload gateway configurations
sofia profile fax rescan

# Check gateway status
sofia status gateway pbx_<CUSTOMERNAME>
```

---

## Step 5: Reload Gateway in FreeSwitch

After creating the gateway XML file, scan it in the FreeSwitch CLI:

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

**Response includes an ID** - save this as `<TENANT_ID>` for subsequent steps.

### About the `notify` Field

The notify field specifies destinations for **failed fax notifications**. Multiple destinations can be combined, separated by commas.

**Supported types:**
| Type | Description |
|------|-------------|
| `email_report-><email>` | Email with PDF report only |
| `email_full-><email>` | Email with PDF report + original fax attachment |
| `email_full_failure-><email>` | Like `email_full` but only sent on failed faxes |
| `webhook-><URL>` | HTTP POST with base64-encoded PDF report |
| `webhook_form-><URL>` | Multipart form POST with first page PDF (for n8n) |

**Example:**
```
email_full_failure->support@customer.com,webhook_form->https://n8n.example.com/webhook/123
```

For transcoded/bridged calls, this field is generally **not used**.

---

## Step 7: Add Endpoint

Endpoint format for gateways: `<GATEWAY_XML_NAME>:<PBX_IP>`

### Endpoint Types

| Type | Description | Endpoint Format |
|------|-------------|-----------------|
| `gateway` | SIP gateway to PBX | `xml_name:ip` |
| `webhook` | HTTP POST delivery | Full URL (e.g., `https://...`) |
| `email` | Email delivery | Email address |

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

### Gateway Endpoint (Transcoding/Bridge Mode - Recommended for New Customers)

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

For HTTP-based fax delivery:

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

For email-based fax delivery:

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

### Priority Method

The `priority` field controls failover behavior:

| Priority Value | Behavior |
|----------------|----------|
| `0` | Highest priority (primary endpoint) |
| `1+` | Lower priority (failover endpoints) |
| `666` | Special value - endpoint does not receive inbound faxes |

**How priority works:**
- Lower numbers = higher preference
- If the primary endpoint (priority 0) fails, the system attempts the next highest priority
- Priority 666 is used for source gateways in outbound-only bridge scenarios

**Example - Gateway with Failover:**
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

### Endpoint Scope

Endpoints can be scoped at different levels:

| Type | Description |
|------|-------------|
| `tenant` | Applies to all numbers in the tenant |
| `number` | Applies to a specific number only (takes precedence over tenant) |
| `global` | Fallback for all tenants when no tenant/number endpoint exists |

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
    "header": "<FAX_HEADER>"
  }'
```

Add each number with a separate command.

---

## Step 9: Reload Configuration

```bash
curl -X GET http://<FAX_SERVER_HOST>:8080/admin/reload \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)"
```

This hot-reloads tenants, numbers, and endpoints from the database into memory.

---

## Step 10: Configure SBC Digitmap (If Applicable)

If using a Session Border Controller:

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
3. Verify logs appear in Grafana dashboard

---

## Step 12: Monitoring

- Check active fax jobs: `curl http://<FAX_SERVER_HOST>:8080/admin/faxes`
- View fax status by UUID: `curl "http://<FAX_SERVER_HOST>:8080/fax/status?uuid=<JOB_UUID>"`
- FreeSWITCH logs: `fs_cli` with `sofia global siptrace on` for debugging

---

## Python TUI (Optional)

For an interactive terminal-based setup wizard, use the Python TUI script:

```bash
# Install dependencies
pip install prompt_toolkit requests

# Set environment variables
export FAX_API_URL=http://<FAX_SERVER>:8080
export FAX_API_KEY=<your_api_key>

# Run the TUI
python scripts/setup-tenant-tui.py
```

The TUI provides a step-by-step wizard for creating tenants, endpoints, and numbers. FreeSWITCH gateway configuration must still be done manually.

---

## Related Documentation

- [TENANTS.md](TENANTS.md) - Detailed tenant modes and configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) - System architecture
- [API_REFERENCE.md](API_REFERENCE.md) - Full API documentation
- [GATEWAYS.md](GATEWAYS.md) - FreeSWITCH gateway configuration details
