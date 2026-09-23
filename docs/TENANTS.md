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
    Notify  string         `json:"notify"` // notify list, comma-separated: type->dest,type->dest
    Numbers []TenantNumber `gorm:"foreignKey:TenantID" json:"numbers"`
}
```

### TenantNumber

A phone number assigned to a tenant.

```go
// gofaxserver/tenants.go:26-33
type TenantNumber struct {
    ID       uint   `gorm:"primaryKey" json:"id"`
    TenantID uint   `gorm:"index;not null" json:"tenant_id"`
    Number   string `gorm:"unique;not null" json:"number"` // 10 digits or whatever format matches the dialplan transforms
    Name     string `json:"name"`     // Caller ID name
    Header   string `json:"header"`   // Fax header displayed at top
    Notify   string `json:"notify"`   // Per-number override; falls back to tenant-level if empty
}
```

### Endpoint

A connection point for fax routing.

```go
// gofaxserver/endpoints.go:23-31
type Endpoint struct {
    ID           uint   `gorm:"primaryKey" json:"id"`
    Type         string `json:"type"`          // "tenant", "number", or "global"
    TypeID       uint   `json:"type_id"`       // Tenant ID, TenantNumber ID, or 0
    EndpointType string `json:"endpoint_type"` // "gateway", "webhook", "email", "portal"
    Endpoint     string `json:"endpoint"`      // gateway: "xml_name:publicIP"; webhook: URL; email: addr; portal: svc_username
    Priority     uint   `json:"priority"`      // Lower = higher priority
    Bridge       bool   `json:"bridge"`        // Enable T.38/G.711 transcoding
}
```

**Type values:**
- `type` ∈ `{tenant, number, global}`
- `endpoint_type` ∈ `{gateway, webhook, email, portal}`

**Priority 666** — special value meaning "do not receive inbound faxes". The endpoint is filtered out of `getEndpointsForNumber` (inbound delivery) but is still present in `getEndpointsForBridge` for use as a source gateway in outbound bridge calls.

**Priority 999** — reserved for the implicit upstream-fallback group the Router synthesizes when no tenant/number endpoint matches (see [Architecture](ARCHITECTURE.md)).

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

> **Note:** gateway endpoints created via gateway provisioning
> (`POST /admin/gateway` or the portal's Gateways tab) are *managed* — direct
> `PUT/DELETE /admin/endpoint/{id}` on them is refused with 409. Update or
> deprovision them through the gateway API instead.

**Router behavior** (`gofaxserver/router.go:31-231`):
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

**Router/bridge behavior** (`gofaxserver/router.go:250-313` and `freeswitch_inbound.go`):
- `detectAndRouteToBridge` returns `(bridgeGateway, true)` when the call came from an upstream gateway AND the destination has a bridge-enabled tenant/number endpoint.
- The inbound leg is bridged to the destination gateway with `t38_gateway self nocng` on either side, transparently transcoding between T.38 and G.711.
- Falls back to G.711 softmodem if T.38 negotiation fails (Postgres-backed fax policy rules + persisted per-pair flip-flop probing).

---

## T.38 Negotiation Strategy

T.38/ECM/V.17 policy is resolved per call by the Postgres-backed fax policy
engine (`gofaxserver/faxpolicy.go`):

1. **Policy rules first** — manual and auto-learned rules from the
   `fax_policy_rules` table are resolved (pair > dst > src, manual > auto,
   off beats on). Rules can target softmodem and bridged (transcoded) calls
   independently (`applies_to`), so e.g. T.38 can be disabled for softmodem
   calls to a destination while bridged calls to the same destination still
   use T.38.
2. **Auto-learning** — softmodem-path failures (repeated negotiations, bad
   rows, T.38 refusals) escalate automatically: `t38_off` → `ecm_off` →
   `v17_off`; consecutive successes or expiry heal the rules (re-probing).
   A learned `t38_off` applies to both call types so a destination known not
   to support T.38 is never offered it (including far-end re-INVITEs).
3. **Upstream gateways only** — T.38 enabled only for calls to/from carriers; tenant/peer gateways always run G.711.
4. **Flip-flop probing** — when no rule decides T.38, the persisted per-pair
   state (`fax_pair_states`, separate for softmodem/bridge) alternates T.38
   on/off within the `pair_state_ttl` window (default 15m). Mainly relevant
   for bridged calls, which have no fax result telemetry.

Rules are managed via `GET/POST/PUT/DELETE /admin/fax-policies` (see
[ARCHITECTURE.md](ARCHITECTURE.md)) and the portal's "Fax Policies" admin view.

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

Endpoints can be assigned directly to a number (not just tenant-level). `type_id` is the `TenantNumber.ID` (DB primary key, **not** the phone-number string):

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

Number-specific endpoints take precedence over tenant endpoints (`tenants.go:196-231` — number scope is checked first).

---

## Global Upstream Endpoints

Upstream carrier gateways shared by all tenants. Registered with `type=global, type_id=0` via the same `/admin/endpoint` API:

```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic ..." \
  -d '{
    "type": "global",
    "type_id": 0,
    "endpoint_type": "gateway",
    "endpoint": "upstream_carrier:198.51.100.10",
    "priority": 0
  }'
```

Global gateway endpoints are loaded into `Server.UpstreamFsGateways` and used as a fan-out fallback when no tenant/number endpoint matches. The Queue treats them as a single aggregated group with `isGlobalUpstreamGatewayGroup` detection — see `queue.go:64-76, 275-364`.

> **Why `endpoint` includes the IP even for upstream?** gofaxserver's `fsGatewayACL` matches inbound source IP against the `Endpoint` value, so the `xml_name:publicIP` format is required for inbound ACL to pass.

---

## Notification Configuration

The `notify` field on a tenant and on each tenant number specifies destinations for fax completion notifications. The field is parsed by `gofaxserver/notify.go:parseNotifyString` (split on commas → split on `->`).

### When Notifications Fire (Direction Semantics)

Notifications fire when a queued fax job **concludes (after all retries)**, regardless of direction:

- **Outbound transmissions** (e.g. `POST /fax/send`) resolve destinations from the **source side**: the *caller* number's `notify` first, falling back to the *source tenant's* `notify` (`notify.go:processNotifyDestinations`, src branch; `SrcTenantID` is stamped from the caller number in `router.go:routeFax`).
- **Inbound receptions** resolve from the **destination side**: the *callee* number's `notify` first, falling back to the destination tenant's `notify`.
- Both sides receive notifications for the same job when both are known tenants/numbers.

`email_report` is sent on job completion **whether the attempts succeeded or failed** — the attached report rows show per-attempt status. Only `email_full_failure` gates on failure.

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
| `webhook->` | HTTP POST with JSON payload | Base64-encoded PDF report + fax job data as JSON |
| `webhook_form->` | Multipart form POST | First page PDF attached to form data |
| `portal->` | HTTP POST to gofaxportal | Compact final-outcome JSON (see below) |

**Notes:**
- `email_report` sends a PDF report with attempt history but no fax attachment; it fires on completion in both directions (success or failure).
- `email_full` includes the actual fax TIFF file as an attachment (large).
- `email_full_failure` is like `email_full` but only triggers on failed faxes (`all_attempts_failed == true`).
- `webhook` sends a JSON POST with base64-encoded PDF report and fax job data.
- `webhook_form` sends a multipart form POST with the first page of the fax as a PDF attachment (useful for n8n workflows).
- `portal` sends a compact JSON status push to `portal.url + /portal/api/notify/<destination>`, where the destination is the portal org's service-account username (the portal manages this entry itself via its number assignments — see [PORTAL.md](PORTAL.md)). Carries no file data; requires `portal.url` (and `portal.api_key` when the portal has `inbound_api_key` set) in `config.json`.
- Unknown types (e.g. a bare `email->` per the inline comment in `tenants.go:13`) are logged and dropped — only the documented types above are dispatched.

### Multiple Recipients

Multiple email addresses for the **same** notification type are separated by `;` — only valid for `email_*` types (the SMTP layer splits on `;` at `notify.go:304-307`):

```
email_report->addr1@customer.com;addr2@customer.com
```

Multiple webhooks require separate `webhook->` entries, comma-separated.

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

Notifications are processed asynchronously after fax completion. Number-level `notify` takes precedence over tenant-level; an empty number-level falls back to the tenant's `notify`.

```go
// gofaxserver/notify.go:153-206
func (q *Queue) processNotifyDestinations(f *FaxJob) ([]NotifyDestination, error)
```

---

## Related Documentation

- [INSTALLATION.md](INSTALLATION.md) — Full installation guide
- [SETUP.md](SETUP.md) — Step-by-step setup procedure
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
- [API_REFERENCE.md](API_REFERENCE.md) — Full API documentation
- [GATEWAYS.md](GATEWAYS.md) — FreeSWITCH gateway configuration
- [PORTAL.md](PORTAL.md) — Web portal (uses notify strings for user receipts)
