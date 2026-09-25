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

The optional `notify` field overrides the tenant-level `notify` for faxes involving this number. See [TENANTS.md](TENANTS.md#supported-notification-types) for the supported destination types (`email_report`, `email_full`, `email_full_failure`, `webhook`, `webhook_form`, `portal`).

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
| `endpoint_type` | string | `"gateway"`, `"webhook"`, `"email"`, or `"portal"` |
| `endpoint` | string | `xml_name:publicIP` for gateways, URL for webhooks, email address for email, portal org's svc_username for portal |
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

**Note:** endpoints linked to a provisioned gateway
(`gateway_configs.endpoint_id`) cannot be updated or deleted through these
routes — the request is refused with 409 and a pointer to
`/admin/gateway/{name}`. This keeps gateways from losing their endpoint rows.

---

### Gateway Provisioning (FreeSWITCH)

API-driven gateway provisioning: renders a database-backed template, writes
the gateway XML to `freeswitch.gateway_config_dir`, reloads the `fax` sofia
profile over the event socket, and manages the linked endpoint. Returns 500
with a clear error when `gateway_config_dir` is not configured.

Gateway `params` are stored encrypted (AES-256 with `psk`) and secret values
(`password`, anything containing `password`/`secret`) are masked as
`"********"` in all responses. On update, leaving a secret as `"********"`
keeps the previously stored value.

#### List Gateways

```
GET /admin/gateways
```

**Response:** `{gateways: [...], unmanaged: [...]}` — each gateway entry is
`{gateway, file, state, exists_on_disk}` where `state` is parsed from
`sofia status gateway <name>` and registered gateways also carry
`gateway.last_state` from the monitor. `unmanaged` lists XML files on disk
with no DB record: `[{file, name}]`.

#### Adopt / Delete Unmanaged Gateway Files

```
POST   /admin/gateways/adopt                    {"file": "pbx_acme.xml"}
DELETE /admin/gateways/unmanaged/{name}?confirm=true
```

Adopt records a `gateway_configs` row for an existing file (no template —
assign one via update). Delete removes the file and tears the gateway down.

#### Repair Gateway

```
POST /admin/gateway/{name}/repair
```

Re-renders the gateway XML from its stored template + params (restores a
missing or out-of-band-edited file) and reloads FreeSWITCH.


#### Provision Gateway (combined XML + endpoint)

```
POST /admin/gateway
```

**Payload:**

```json
{
  "name": "pbx_acme",
  "template_id": 2,
  "params": { "realm": "192.0.2.10", "register": false },
  "type": "tenant",
  "type_id": 7,
  "priority": 0,
  "bridge": true,
  "endpoint_ip": ""
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Gateway name (`^[A-Za-z0-9_]+$`); equals the XML filename and endpoint prefix |
| `template_id` | uint | ID of a gateway template (see below) |
| `params` | object | Template variables; `realm` required, `register=true` requires `username`+`password` |
| `type` | string | Linked endpoint scope: `tenant`, `number`, or `global` |
| `type_id` | uint | Tenant/number ID (0 for `global`) |
| `priority` | uint | Endpoint priority (`666` = outbound-only) |
| `bridge` | bool | Bridge/transcoding mode instead of txfax/rxfax |
| `endpoint_ip` | string | ACL address (IP or hostname) for the endpoint value; defaults to `realm`. Hostname entries are resolved via DNS and re-resolved on every gateway monitor tick |

On failure at any step, prior steps roll back (file removed, gateway torn
down, endpoint deleted).

#### Update Gateway

```
PUT /admin/gateway/{name}
```

Same payload as provision, except `type`/`type_id`/`priority`/`bridge` may be
omitted — the linked endpoint's scope, priority and bridge flag are preserved
regardless; only its `name:ip-or-hostname` value tracks realm/`endpoint_ip`
changes (the in-memory ACL and its DNS resolution are reloaded immediately).
Gateways cannot be renamed — delete and re-provision instead. Adopted gateways
(no linked endpoint) never gain one through update.

#### Deprovision Gateway

```
DELETE /admin/gateway/{name}
```

Kills the gateway (`sofia killgw fax <name>`), removes the XML, rescans the
profile, and deletes the linked endpoint.

#### Provision Gateway From Existing Endpoint

```
POST /admin/gateways/from-endpoint
```

Brings an existing unmanaged `gateway`-type endpoint (e.g. a routing row from
an imported database) under gateway management. The endpoint value
(`name:ip`) supplies the gateway name and default `realm`; the template is
rendered and written, FreeSWITCH is reloaded, and a `gateway_configs` row is
linked to the **existing** endpoint (no new endpoint is created, and the
endpoint's scope/priority/bridge are untouched).

**Payload:**

```json
{
  "endpoint_id": 12,
  "template_id": 2,
  "params": { "realm": "", "register": false }
}
```

`params.realm` defaults to the IP part of the endpoint value; if the endpoint
has no IP (`name` only), `realm` is required. Fails with an error when the
endpoint is already managed, is not `endpoint_type=gateway`, or its name
prefix is not a valid gateway name.

#### FreeSWITCH Profile Control

```
POST /admin/freeswitch/rescan
POST /admin/freeswitch/profile/restart?confirm=true
```

`rescan` runs `reloadxml` + `sofia profile <gateway_profile> rescan` — safe,
no impact on active calls. `restart` runs `sofia profile <gateway_profile>
restart`, which drops all active calls on the profile, and therefore requires
the `confirm=true` query parameter.

### Gateway Templates

Templates use Go `text/template` syntax (`{{.realm}}`, `{{if .register}}…{{end}}`).
Seeded on first start with `sbc` and `pbx` (mirrors of
`examples/freeswitch/gateways/`).

#### List Templates

```
GET /admin/gateway/templates
```

**Response:** array of templates, each with a `variables` array extracted
from the body (for dynamic form rendering).

#### Create / Update / Delete Template

```
POST   /admin/gateway/templates         {name, description, body}
PUT    /admin/gateway/templates/{id}    {name, description, body}
DELETE /admin/gateway/templates/{id}
```

Bodies are validated (parseable template) on write. Delete is refused while
provisioned gateways reference the template.

---

### Dialplan Rules

Number transformation rules applied to caller/callee numbers before tenant
lookup and routing. Source is selected by `dialplan.source` in config.json:

- `config` (default) — rules come from `dialplan.rules` in config.json, or the
  built-in defaults when the section is absent. The CRUD routes below still
  work but the stored rules are **inactive** until the source is switched.
- `db` — rules come from the `dialplan_rules` table, hot-reloadable: every
  mutation below takes effect immediately, and `/admin/reload` also refreshes
  them. On first run the table is seeded from the config rules (or defaults).

#### Get Dialplan

```
GET /admin/dialplan
```

**Response:** `{"source": "config"|"db", "rules": [...]}` ordered by position.

#### Create / Update / Delete Rule

```
POST   /admin/dialplan/rules           {pattern, replacement, position?, enabled, description}
PUT    /admin/dialplan/rules/{id}      {pattern, replacement, position, enabled, description}
DELETE /admin/dialplan/rules/{id}
```

Patterns are regex-validated on write. Replacement supports capture groups
(`$1`, `$2`, …). Position defaults to the end of the list on create.

#### Reorder Rules

```
POST /admin/dialplan/rules/reorder     [{id, position}, ...]
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

### Fax Policy Rules (T.38 / ECM / V.17)

Postgres-backed policy rules replacing the old FreeSWITCH `mod_db` softmodem
fallback. Rules are composable and hot-reloaded on every write.

```
GET    /admin/fax-policies?number=&scope=&effect=&origin=&applies_to=
POST   /admin/fax-policies
PUT    /admin/fax-policies/{id}
DELETE /admin/fax-policies/{id}
POST   /admin/fax-policies/{id}/expire
GET    /admin/fax-policies/resolve?src=&dst=&type=softmodem|bridge
GET    /admin/fax-policies/pair-states?number=
DELETE /admin/fax-policies/pair-states/{id}
```

**Rule payload:**
```json
{
  "scope": "dst",              
  "dst_number": "5551234567",
  "effect": "t38_off",         
  "applies_to": "both",        
  "enabled": true,
  "expires_at": null,          
  "notes": "carrier X refuses T.38 re-INVITE"
}
```

- `scope`: `dst` (destination only), `src` (source only), or `pair` (specific src→dst, requires both numbers) — pair rules let one sender be affected without impacting other senders to the same destination.
- `effect`: `t38_off`, `t38_on`, `ecm_off`, `ecm_on`, `v17_off`, `softmodem_only`, `var_override`.
- `applies_to`: `both`, `softmodem` (non-bridged txfax/rxfax), or `bridge` (transcoded) — softmodem and bridged calls are controlled independently.
- `expires_at`: optional RFC3339 timestamp for temporary rules.
- `var_name` / `var_value`: required when `effect=var_override` — overrides a channel variable in the outbound dialstring (replacing the old mod_db `override-<number>` realm). `var_name` must match `[A-Za-z0-9_]+`; `origination_uuid` cannot be overridden. Cleared/ignored on all other effects.

Resolution order: per attribute, most specific scope wins (pair > dst > src),
`manual` beats `auto`, and at equal specificity "off" beats "on". For
`var_override`, the most specific rule wins *per variable name* and different
names from different scopes merge; the resolved set is returned as
`policy.var_overrides` and applied to the outbound dialstring. Rules with
`origin=auto` are learned from fax failures and heal after consecutive
successes or expiry. The `resolve` endpoint is a dry run returning the
effective policy and applied rule set for a src/dst/call-type.

### Softmodem Fallback (deprecated)

```
POST /admin/fallback
```

**Payload:**
```json
{
  "number": "5551234567"
}
```

Deprecated shim for the old mod_db fallback: now creates a manual dst-scoped
`t38_off` fax policy rule (applies to both call types). Prefer
`POST /admin/fax-policies`.

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

**Response:** array of `fax_job_results` rows. Multiple rows may be returned for a single job UUID when retries occurred. Portal/API-submitted jobs also include a `submission` row (attempt_number 0, hangup_cause `WEBHOOK`) recording the job's intake — it is not a real attempt and must be ignored for terminal-state decisions:

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
    "gateway": "carrier_sbc1",
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

- [INSTALLATION.md](INSTALLATION.md) — Full installation guide
- [SETUP.md](SETUP.md) — Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) — Tenant and endpoint configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
- [GATEWAYS.md](GATEWAYS.md) — FreeSWITCH gateway configuration
