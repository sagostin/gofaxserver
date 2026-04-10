# API Reference

Complete reference for the gofaxserver REST API.

## Authentication

### Admin API

- **Method:** Basic Authentication
- **Credentials:** `admin:<API_KEY>`
- **Header:** `Authorization: Basic <base64("admin:api_key")>`

### Tenant User API

- **Method:** Basic Authentication  
- **Credentials:** `<username>:<password>`
- **Header:** `Authorization: Basic <base64("username:password")>`

---

## Admin Endpoints

All admin endpoints require Basic Authentication with the admin API key.

### Reload Configuration

Hot-reload tenants, numbers, and endpoints from database into memory.

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

**Response:**
```json
{
  "active": 2,
  "items": [
    {
      "job_uuid": "f0c648ff-702a-43e9-b8a3-d0ebffe70cd9",
      "phase": "BRIDGING",
      "caller": "2364224783",
      "callee": "6042339777",
      "endpoint_label": "upstream",
      "started_at": "2025-12-23T12:50:52Z"
    }
  ]
}
```

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

**Example:**
```bash
curl -X POST http://<HOST>:8080/admin/number \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": 1,
    "number": "5551234567",
    "name": "Main Fax",
    "header": "ACME Corp"
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

**Payload:**
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
| type | string | `"tenant"`, `"number"`, or `"global"` |
| type_id | uint | Tenant ID or Number ID |
| endpoint_type | string | `"gateway"`, `"webhook"`, or `"email"` |
| endpoint | string | Format: `xml_name:ip` for gateways, URL for webhooks/emails |
| priority | uint | Lower = higher priority |
| bridge | bool | Enable T.38/G.711 transcoding |

**Relay Mode Example:**
```bash
curl -X POST http://<HOST>:8080/admin/endpoint \
  -H "Authorization: Basic ..." \
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
  -H "Authorization: Basic ..." \
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

---

#### Update User

```
PUT /admin/user/{id}
```

---

#### Delete User

```
DELETE /admin/user/{id}
```

---

### Softmodem Fallback

Manually set a number to use G.711 only (bypass T.38).

```
POST /admin/fallback
```

**Payload:**
```json
{
  "number": "5551234567"
}
```

---

## Fax Endpoints

All fax endpoints require Basic Authentication with tenant user credentials.

### Send Fax

```
POST /fax/send
```

**Form Data:**
| Field | Type | Description |
|-------|------|-------------|
| file | file | PDF, TIFF, or TIF document |
| caller_number | string | Source fax number |
| callee_number | string | Destination fax number |

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

**Response:**
```json
[
  {
    "id": 1,
    "job_uuid": "550e8400-e29b-41d4-a716-446655440000",
    "result_text": "SUCCESS",
    "success": true,
    "start_ts": "2025-12-23T12:50:52Z",
    "end_ts": "2025-12-23T12:52:10Z"
  }
]
```

---

### Authenticate Tenant User

```
POST /tenant/user/authenticate
```

**Response:**
```json
{
  "message": "authentication successful",
  "api_key": "user_api_key",
  "user_id": 1,
  "tenant_id": 1
}
```

---

## Error Responses

| Status | Meaning |
|--------|---------|
| 400 | Bad Request - Invalid input or missing parameters |
| 401 | Unauthorized - Invalid or missing credentials |
| 500 | Internal Server Error - Server-side failure |

---

## Related Documentation

- [SETUP.md](SETUP.md) - Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) - Tenant and endpoint configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) - System architecture
- [GATEWAYS.md](GATEWAYS.md) - FreeSWITCH gateway configuration
