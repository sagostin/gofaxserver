# Tenant Configuration Guide

This document details the tenant data model, endpoint types, and configuration modes.

## Data Models

### Tenant

Represents a customer organization.

```go
// gofaxserver/tenants.go:10-15
type Tenant struct {
    ID      uint           `gorm:"primaryKey" json:"id"`
    Name    string         `json:"name"`
    Notify  string         `json:"notify"` // email_report->...,webhook_form->...,etc.
    Numbers []TenantNumber `gorm:"foreignKey:TenantID" json:"numbers"`
}
```

### TenantNumber

A phone number assigned to a tenant.

```go
// gofaxserver/tenants.go:22-28
type TenantNumber struct {
    ID       uint   `gorm:"primaryKey" json:"id"`
    TenantID uint   `gorm:"index;not null" json:"tenant_id"`
    Number   string `gorm:"unique;not null" json:"number"`
    Name     string `json:"name"`     // Caller ID name
    Header   string `json:"header"`   // Fax header displayed at top
    Notify   string `json:"notify"`   // Per-number notification settings
}
```

### Endpoint

Connection point for fax routing.

```go
// gofaxserver/endpoints.go
type Endpoint struct {
    ID            uint   `gorm:"primaryKey" json:"id"`
    Type          string `json:"type"`           // "tenant", "number", "global"
    TypeID        uint   `json:"type_id"`        // TenantID or TenantNumber ID
    EndpointType  string `json:"endpoint_type"`  // "gateway", "webhook", "email"
    Endpoint      string `json:"endpoint"`       // "xml_name:ip" or URL
    Priority      uint   `json:"priority"`       // Lower = higher priority
    Bridge        bool   `json:"bridge"`         // Enable transcoding
}
```

**Endpoint type values:**
- `type`: `"tenant"` | `"number"` | `"global"`
- `endpoint_type`: `"gateway"` | `"webhook"` | `"email"`

**Priority 666**: Special value meaning "do not receive inbound faxes". Used for source gateways in outbound bridge scenarios.

---

## Integration Modes

### Relay Mode

Faxes pass through without transcoding. T.38 is passed end-to-end when both sides support it.

**When to use:**
- Both PBX and carrier support T.38
- Want lowest latency, no quality loss from transcoding
- Existing setup where both ends are T.38-capable

**Configuration:**
```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic ..." \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "gateway",
    "endpoint": "pbx_<NAME>:<PBX_IP>",
    "priority": 0
  }'
```

**Router behavior** (`gofaxserver/router.go:65-85`):
- Incoming fax from upstream → lookup endpoint for destination number
- Endpoint found → queue fax directly to that gateway
- No transcoding occurs

---

### Transcoding/Bridge Mode

Converts between T.38 and G.711 audio. Enables fax communication between endpoints with different capabilities.

**When to use:**
- PBX does NOT support T.38 (only G.711)
- Customer has FXS gateway for physical fax machines
- Mixed T.38/G.711 environment
- **Recommended default for new customers**

**Configuration:**
```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic ..." \
  -d '{
    "type": "tenant",
    "type_id": <TENANT_ID>,
    "endpoint_type": "gateway",
    "endpoint": "pbx_<NAME>:<PBX_IP>",
    "priority": 0,
    "bridge": true
  }'
```

**Router behavior** (`gofaxserver/router.go:130-175`):
- Detects if call came from upstream via `UpstreamFsGateways` list
- If `bridge: true` endpoint found, initiates T.38/G.711 conversion
- Falls back to G.711 softmodem if T.38 negotiation fails

---

## T.38 Negotiation Strategy

The system implements intelligent T.38 flip-flop:

1. **Upstream gateways only** - T.38 enabled only for calls to/from carriers
2. **Flip-flop retry** - First call to number pair uses T.38; retry within 15 minutes uses G.711
3. **Softmodem fallback** - Numbers with repeated T.38 failures automatically switch to G.711
4. **Local endpoints** - Calls to/from tenant gateways always use G.711

```go
// gofaxserver/server.go:47-61
const (
    T38PairTTL = 15 * time.Minute
)
```

---

## Priority-Based Routing

Endpoints support priority for failover:

```json
{
  "endpoint": "pbx_primary:192.168.1.10",
  "priority": 0
},
{
  "endpoint": "pbx_secondary:192.168.1.11",
  "priority": 1
}
```

Lower priority number = higher preference. If primary fails, secondary is tried.

---

## Number-Specific Endpoints

Endpoints can be assigned directly to a number (not just tenant-level):

```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic ..." \
  -d '{
    "type": "number",
    "type_id": <TENANT_NUMBER_ID>,
    "endpoint_type": "gateway",
    "endpoint": "special_gw:<IP>",
    "priority": 0
  }'
```

Number-specific endpoints take precedence over tenant endpoints.

---

## Global Endpoints

For upstream carrier gateways used by all tenants:

```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic ..." \
  -d '{
    "type": "global",
    "type_id": 0,
    "endpoint_type": "gateway",
    "endpoint": "upstream_carrier",
    "priority": 999
  }'
```

These are fallback endpoints when no tenant/number-specific endpoint exists.

---

## Notification Configuration

The notify field specifies destinations for failed fax notifications. Multiple destinations can be configured, separated by commas.

### Notify Field Format

```
<type>-><destination>,<type>-><destination>
```

**Example:**
```
email_report->support@customer.com,webhook_form->https://n8n.example.com/webhook/123,email_full_failure->fax-failures@customer.com
```

### Supported Notification Types

| Type | Description | Content |
|------|-------------|---------|
| `email_report->` | Email with PDF report only | Fax result report (attempts, status, timestamps) |
| `email_full->` | Email with report + original fax | PDF report + original fax TIFF file attached |
| `email_full_failure->` | Like `email_full` but only on failure | Only sent when fax fails; original fax included |
| `webhook->` | HTTP POST with JSON payload | Base64-encoded PDF report + fax data as JSON |
| `webhook_form->` | Multipart form POST | First page PDF attached to form data |

**Notes:**
- `email_report` sends a PDF report with attempt history but no fax attachment
- `email_full` includes the actual fax TIFF file as an attachment (large)
- `email_full_failure` is like `email_full` but only triggers on failed faxes
- `webhook` sends a JSON POST with base64-encoded PDF report and fax job data
- `webhook_form` sends a multipart form POST with the first page of the fax as a PDF attachment (useful for n8n workflows)

### Multiple Recipients

Separate multiple email addresses with semicolons:
```
email_report->addr1@customer.com;addr2@customer.com
```

### Tenant-Level Notifications

Applied to all numbers under a tenant unless overridden:

```json
{
  "name": "Customer Name",
  "notify": "email_report->support@customer.com,webhook_form->https://n8n.example.com/webhook/123"
}
```

### Number-Level Notifications

Override tenant notifications for specific numbers:

```bash
curl -X PUT http://<FAX_SERVER>:8080/admin/number/<ID> \
  -H "Authorization: Basic ..." \
  -d '{
    "tenant_id": <TENANT_ID>,
    "number": "5551234567",
    "name": "Main Fax",
    "header": "ACME Corp",
    "notify": "email_full_failure->fax-failures@acme.com"
  }'
```

### Notification Processing

Notifications are processed asynchronously after fax completion:

```go
// gofaxserver/notify.go:152-205
func (q *Queue) processNotifyDestinations(f *FaxJob) ([]NotifyDestination, error)
```

Priority: Number-level notify > Tenant-level notify

If a number has an empty notify field, it falls back to the tenant's notify settings.

---

## Related Documentation

- [SETUP.md](SETUP.md) - Step-by-step setup procedure
- [ARCHITECTURE.md](ARCHITECTURE.md) - System architecture
- [API_REFERENCE.md](API_REFERENCE.md) - Full API documentation
