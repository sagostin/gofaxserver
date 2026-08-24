# API Reference

Complete reference for the gofaxserver REST API. All routes are registered in `gofaxserver/web.go:loadWebPaths`.

## Authentication

| Group | Auth Method | Credentials |
|-------|-------------|-------------|
| Admin API (`/admin/*`) | Basic Auth | `username:<web.api_key>` (any username; the password **is** the admin API key) |
| Tenant User API (`/fax/*`) | Basic Auth | `<username>:<password>` (tenant user credentials) |
| Self-service (`/tenant/user/authenticate`) | Basic Auth | `<username>:<password>` — handler validates and returns `api_key`, `user_id`, `tenant_id` |

`/tenant/user/authenticate` has no middleware — the handler reads and validates the `Authorization` header itself.

`/fax/*` endpoints use the `basicTenantUserAuthMiddleware`, which decrypts the supplied password with `psk` from the config and compares to the stored `tenant_users.password`. On success it stores `tenantID` and `userID` in the Iris context for downstream handlers.

`/health` is unauthenticated and returns 200.

---

## Admin Endpoints

All `/admin/*` endpoints require Basic Auth with the admin API key (from `web.api_key` in config).

### Reload Configuration

Hot-reload tenants, numbers, and endpoints from the database into memory.

```
GET /admin/reload
```

**Example:**
```bash
curl -X GET http://<HOST>:8080/admin/reload \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)"
```

**Response:**
```json
{"message": "Data reloaded successfully"}
```

---

### List Active Faxes

```
GET /admin/faxes
```

**Response:** (`gofaxserver/faxtracker.go:FaxRunState`)

```json
{
  "active": 2,
  "items": [
    {
      "job_uuid": "f0c648ff-702a-43e9-b8a3-d0ebffe70cd9",
      "call_uuid": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
      "src_tenant_id": 1,
      "dst_tenant_id": 2,
      "caller": "2364224783",
      "callee": "6042339777",
      "phase": "BRIDGING",
      "attempt": 1,
      "max_attempts": 3,
      "current_priority": 0,
      "endpoint_type": "gateway",
      "endpoint_label": "upstream",
      "endpoint_value": "sbc_carrier1",
      "last_error": "",
      "last_result": "OK",
      "result_success": true,
      "enqueued_at": "2025-12-23T12:50:50Z",
      "started_at": "2025-12-23T12:50:52Z",
      "updated_at": "2025-12-23T12:51:30Z"
    }
  ]
}
```

Phases: `ROUTED`, `ATTEMPTING`, `RECEIVING`, `BRIDGING`, `SENDING`, `WAITING`, `DONE`.

---

### List Resources (Read-Only)

Read-only listing endpoints intended for reconciliation by external tools (e.g. the portal). All live under `/admin` auth and never mutate state.

#### List Tenants

```
GET /admin/tenants
```

**Response:** array of tenants, each with its numbers preloaded:

```json
[
  {
    "id": 1,
    "name": "Acme Corp",
    "notify": "",
    "numbers": [
      { "id": 1, "tenant_id": 1, "number": "5551234567", "name": "Main Fax", "header": "ACME Corp", "notify": "" }
    ]
  }
]
```

#### List Numbers

```
GET /admin/numbers[?tenant_id=<ID>]
```

**Response:** flat array of `tenant_numbers` rows, optionally filtered by tenant.

#### List Users

```
GET /admin/users[?tenant_id=<ID>]
```

**Response:** array of users with the encrypted password **redacted**:

```json
[
  { "id": 1, "tenant_id": 1, "username": "svc_acme", "api_key": "..." }
]
```

#### List Endpoints

```
GET /admin/endpoints[?type=<tenant|number|global>][&type_id=<ID>]
```

**Response:** flat array of `endpoints` rows, optionally filtered by scope type and/or type_id.

---

### Tenant Management

#### Create Tenant

```
POST /admin/tenant
```

**Payload:**
```json
{
  "name": "Customer Name",
  "notify": "webhook_form->https://...,email_report->support@customer.com"
}
```

**Example:**
```bash
curl -X POST http://<HOST>:8080/admin/tenant \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme Corp","notify":""}'
```

**Response:**
```json
{
  "id": 1,
  "name": "Acme Corp",
  "notify": ""
}
```

---

#### Update Tenant

```
PUT /admin/tenant/{id}
```

**Payload:**
```json
{
  "name": "Updated Name",
  "notify": "email_report->new@customer.com"
}
```

---

#### Delete Tenant

```
DELETE /admin/tenant/{id}
```

---

### Number Management

#### Add Number

```
POST /admin/number
```

**Payload:**
```json
{
  "tenant_id": 1,
  "number": "5551234567",
  "name": "Main Fax",
  "header": "ACME Corp",
  "notify": "email_full_failure->fax@acme.com"
}
```

The optional `notify` field overrides the tenant-level `notify` for faxes involving this number.

**Example:**
```bash
curl -X POST http://<HOST>:8080/admin/number \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": 1,
    "number": "5551234567",
    "name": "Main Fax",
    "header": "ACME Corp",
    "notify": "email_full_failure->fax@acme.com"
  }'
```

---

#### Update Number

```
PUT /admin/number/{id}
```

---

#### Delete Number

```
DELETE /admin/number?number=<NUMBER>&tenant_id=<TENANT_ID>
```

**Example:**
```bash
curl -X DELETE "http://<HOST>:8080/admin/number?number=5551234567&tenant_id=1" \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)"
```

---

### Endpoint Management

#### Add Endpoint

```
POST /admin/endpoint
```

**Payload (tenant scope, gateway type):**
```json
{
  "type": "tenant",
  "type_id": 1,
  "endpoint_type": "gateway",
  "endpoint": "pbx_acme:192.168.1.100",
  "priority": 0,
  "bridge": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `"tenant"`, `"number"`, or `"global"` |
| `type_id` | uint | Tenant ID (tenant scope), TenantNumber ID (number scope), or `0` (global) |
| `endpoint_type` | string | `"gateway"`, `"webhook"`, or `"email"` |
| `endpoint` | string | `xml_name:publicIP` for gateways, URL for webhooks, email address for email |
| `priority` | uint | Lower = higher priority; `666` = no inbound delivery; `999` = upstream-fallback |
| `bridge` | bool | Enable T.38/G.711 transcoding |

**Relay Mode Example:**
```bash
curl -X POST http://<HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": 1,
    "endpoint_type": "gateway",
    "endpoint": "pbx_acme:192.168.1.100",
    "priority": 0
  }'
```

**Transcoding/Bridge Mode Example:**
```bash
curl -X POST http://<HOST>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": 1,
    "endpoint_type": "gateway",
    "endpoint": "pbx_acme:192.168.1.100",
    "priority": 0,
    "bridge": true
  }'
```

**Global Upstream Gateway Example:**
```bash
curl -X POST http://<HOST>:8080/admin/endpoint \
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

---

#### Update Endpoint

```
PUT /admin/endpoint/{id}
```

---

#### Delete Endpoint

```
DELETE /admin/endpoint/{id}
```

---

### User Management

#### Create User

```
POST /admin/user
```

**Payload:**
```json
{
  "tenant_id": 1,
  "username": "faxuser",
  "password": "securepassword",
  "api_key": "user_api_key"
}
```

The password is encrypted with the configured `psk` before being stored (`gofaxserver/tenants.go:AddTenantUser`). The `api_key` is stored verbatim — the server does not generate one automatically.

---

#### Update User

```
PUT /admin/user/{id}
```

If the payload includes a non-empty `password`, the password is re-encrypted with `psk` and saved. Other fields (`username`, `tenant_id`, `api_key`) are updated; leaving `password` empty keeps the existing password.

---

#### Delete User

```
DELETE /admin/user/{id}
```

---

### Softmodem Fallback

Manually set a number to use G.711 only (bypass T.38) by writing the `fallback` realm in FreeSWITCH `mod_db`.

```
POST /admin/fallback
```

**Payload:**
```json
{
  "number": "5551234567"
}
```

The flag is consulted on subsequent inbound calls (both bridge and rxfax paths) and disables T.38 for that side.

---

## Tenant-User Endpoints

All `/fax/*` endpoints require Basic Auth with tenant user credentials (validated via `basicTenantUserAuthMiddleware`).

### Send Fax

```
POST /fax/send
```

This is the **only** endpoint that takes `multipart/form-data` (not JSON).

**Form Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `file` | file | PDF, TIFF, or TIF document |
| `caller_number` | string | Source fax number (must belong to the authenticated tenant) |
| `callee_number` | string | Destination fax number |

**Example:**
```bash
curl -X POST http://<HOST>:8080/fax/send \
  -H "Authorization: Basic $(echo -n 'tenantuser:password' | base64)" \
  -F "file=@document.pdf" \
  -F "caller_number=5551234567" \
  -F "callee_number=5559876543"
```

**Response:**
```json
{
  "message": "fax enqueued",
  "job_uuid": "550e8400-e29b-41d4-a716-446655440000"
}
```

The PDF is converted to TIFF via Ghostscript (`gofaxserver/tiff.go:pdfToTiff`) and enqueued via `s.FaxJobRouting <- faxjob`.

---

### Check Fax Status

```
GET /fax/status?uuid=<JOB_UUID>
```

**Example:**
```bash
curl -X GET "http://<HOST>:8080/fax/status?uuid=550e8400-e29b-41d4-a716-446655440000" \
  -H "Authorization: Basic $(echo -n 'tenantuser:password' | base64)"
```

**Response:** array of `fax_job_results` rows. Multiple rows may be returned for a single job UUID when retries occurred:

```json
[
  {
    "id": 1,
    "job_uuid": "550e8400-e29b-41d4-a716-446655440000",
    "callee_number": "5559876543",
    "caller_id_number": "5551234567",
    "result_type": "transmission",
    "attempt_number": 1,
    "endpoint_type": "gateway",
    "start_ts": "2025-12-23T12:50:52Z",
    "end_ts": "2025-12-23T12:52:10Z",
    "hangup_cause": "NORMAL_CLEARING",
    "transferred_pages": 3,
    "success": true,
    "result_text": "OK",
    "t38_status": "negotiated"
  }
]
```

---

## Self-Service Authentication

### Authenticate Tenant User

```
POST /tenant/user/authenticate
```

No middleware; the handler reads the Basic Auth header, decrypts the supplied password with `psk`, and compares.

**Example:**
```bash
curl -X POST http://<HOST>:8080/tenant/user/authenticate \
  -H "Authorization: Basic $(echo -n 'tenantuser:password' | base64)"
```

**Successful Response:**
```json
{
  "message": "authentication successful",
  "api_key": "user_api_key",
  "user_id": 1,
  "tenant_id": 1
}
```

Use the returned `api_key` for further authentication (or keep using the original `username:password` Basic Auth on `/fax/*`).

---

## Error Responses

| Status | Meaning |
|--------|---------|
| 400 | Bad Request — Invalid input or missing parameters |
| 401 | Unauthorized — Invalid or missing credentials |
| 500 | Internal Server Error — Server-side failure |

`/health` returns 200 on every request (no auth required).

---

## Related Documentation

- [SETUP.md](SETUP.md) — Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) — Tenant and endpoint configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
- [GATEWAYS.md](GATEWAYS.md) — FreeSWITCH gateway configuration
