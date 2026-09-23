# Architecture Overview

This document describes the gofaxserver internals: data flow, in-memory state, T.38 negotiation, the database schema, and the HTTP API surface.

A visual companion diagram is available in [`../gofaxserver.excalidraw`](../gofaxserver.excalidraw) (open with <https://excalidraw.com>).

## System Components

```
┌────────────────────────────────────────────────────────────────────┐
│                        gofaxserver                                │
├──────────────┬────────────┬─────────────┬─────────────┬────────────┤
│ Event Socket │  Router    │   Queue     │  Web API    │ FaxTracker │
│   Server     │ + Dialplan │             │  (Iris)     │ LogManager │
│ (in+out)     │  Manager   │             │             │            │
├──────────────┴────────────┴─────────────┴─────────────┴────────────┤
│                          PostgreSQL (GORM)                         │
│  tenants, tenant_numbers, endpoints, tenant_users, fax_job_results │
└────────────────────────────────────────────────────────────────────┘
        │                                            │
        ▼                                            ▼
   FreeSWITCH                                     HTTP Clients
   (mod_spandsp / mod_sofia)                     (Admin / Tenants)
   inbound ESL on :8022
   outbound ESL on :8021
```

### Core Components

| Component | File | Description |
|-----------|------|-------------|
| **Event Socket Server (inbound)** | `gofaxserver/freeswitch_inbound.go` | Listens on `event_server_socket` (default `:8022`). Receives channel events, performs dialplan transforms, decides T.38 strategy, handles `rxfax` / bridge execution. |
| **Event Socket Server (outbound)** | `gofaxserver/freeswitch_outbound.go` | Connects to FreeSWITCH via `event_client_socket` (default `:8021`) to originate calls (`SendFax`) and read per-number channel-var overrides from `mod_db` (realm `override-<number>`). |
| **Router** | `gofaxserver/router.go` | Consumes `FaxJobRouting` channel, resolves tenants, applies priority-based endpoint selection, and enqueues to `Queue`. Bridge detection is in `detectAndRouteToBridge` / `checkForBridge`. |
| **Dialplan Manager** | `gofaxserver/dialplan.go`, `server.go:loadDialplan` | Applies regex transformation rules to caller/callee numbers before tenant lookup. Source is `config` (config.json `dialplan.rules`, or built-in defaults) or `db` (`dialplan_rules` table, hot-reloaded via `/admin/reload` and CRUD writes); the active manager is swapped atomically. |
| **Gateway Provisioner** | `gofaxserver/gateway_provision.go`, `web_gateways.go` | API-driven FreeSWITCH gateway management: DB-backed templates rendered to `freeswitch.gateway_config_dir`, activated via ESL (`reloadxml` + `sofia profile <gateway_profile> rescan`), combined with endpoint creation. Secrets encrypted at rest with `psk`. Tracks DB↔disk drift (unmanaged files, missing files, repair). |
| **Gateway Monitor** | `gofaxserver/gateway_monitor.go` | Periodically polls `sofia status gateway` for provisioned `register=true` gateways, records `last_state`, and logs registration transitions (drops at WARN, recoveries at INFO). Interval: `gateway_monitor_seconds` (default 60, negative disables). |
| **Queue** | `gofaxserver/queue.go` | Manages outbound fax jobs grouped by endpoint type → priority, with exponential backoff retries, jitter, and per-endpoint strategy selection. |
| **Web Server** | `gofaxserver/web.go` | Iris HTTP server on `web.listen` (default `:8080`). Hosts admin, tenant-user, and authenticate parties. |
| **FaxTracker** | `gofaxserver/faxtracker.go` | In-memory state for in-flight jobs; exposed via `GET /admin/faxes`. |
| **LogManager** | `gofaxlib/log.go` | Stdout + Loki dispatcher; all components push structured logs through it. |
| **Fax Policy Engine** | `gofaxserver/faxpolicy.go`, `web_faxpolicy.go` | Postgres-backed T.38/ECM/V.17 policy rules + adaptive learning (replaces the old FreeSWITCH `mod_db` softmodem fallback) and persisted flip-flop pair state. |
| **Notify** | `gofaxserver/notify.go` | Async fan-out of `email_report`, `email_full`, `email_full_failure`, `webhook`, `webhook_form` to a tenant / number's `notify` string. |

---

## Data Flow

### Inbound Fax (External → Customer PBX)

1. Call arrives at FreeSWITCH on the `fax` Sofia profile (`examples/freeswitch/autoload_configs/sofia.conf.xml`).
2. The profile's `dialplan` (`inline:socket:127.0.0.1:8022 async full`) hands the channel to gofaxserver via the inbound ESL.
3. `EventSocketServer.handler` (`freeswitch_inbound.go`) parses the call, applies dialplan transformations, runs the source-IP ACL (`fsGatewayACL` → matches against `GatewayEndpointsACL`), and decides between rxfax and bridge mode.
4. If bridge mode is selected (`detectAndRouteToBridge`), the inbound leg is bridged to the destination endpoint with a T.38 gateway on either leg. If not, the call is answered locally with `rxfax` and the resulting TIFF is placed into the fax temp dir.
5. On hangup the `FaxJob` is enqueued on `FaxJobRouting` for the Router.

### Outbound Fax (Customer → External, or `/fax/send` → External)

1. A fax job is enqueued either via `POST /fax/send` (`web.go:handleDocumentUpload`) or from the inbound path above.
2. The Router (`router.go:routeFax`) resolves src/dst tenants, selects endpoints, and pushes the job to `Queue.Queue`.
3. The Queue (`queue.go:processFax`) groups endpoints by `EndpointType` → `Priority`, then for each group runs the appropriate strategy:
   - **Global upstream fan-out** (`isGlobalUpstreamGatewayGroup` → `Strategy A`): one aggregated FreeSWITCH call, with optional one-shot semantics when bridge is enabled.
   - **Per-endpoint sequential** (`Strategy B`): each gateway is tried individually with retries.
4. `FsSocket.SendFax` (`freeswitch_outbound.go`) originates the call and tracks the result.
5. Results are pushed on `QueueFaxResult`, stored in the `fax_job_results` table, and notifications are dispatched asynchronously.

---

## In-Memory Data Structures

```go
// gofaxserver/server.go
type Server struct {
    FsSocket        *EventSocketServer   // inbound + outbound ESL
    Router          *Router
    Queue           *Queue
    LogManager      *gofaxlib.LogManager
    FaxJobRouting   chan *FaxJob         // inbound + /fax/send jobs into the Router
    DB              *gorm.DB

    dialplan atomic.Pointer[DialplanManager] // active manager; hot-swap in "db" mode — access via Dialplan()

    mu            sync.RWMutex
    Tenants         map[uint]*Tenant             // keyed by Tenant.ID
    TenantNumbers   map[string]*TenantNumber     // keyed by phone number string
    TenantEndpoints map[uint][]*Endpoint         // keyed by tenant ID
    NumberEndpoints map[string][]*Endpoint       // keyed by phone number string
    Endpoints       map[string]*Endpoint         // keyed by "type/endpoint"
    GatewayEndpointsACL []string                 // allowed source IPs for SIP trunks
    UpstreamFsGateways  []string                 // global upstream gateway names

    FaxTracker *FaxTracker

    faxPolicies atomic.Pointer[[]FaxPolicyRule] // active fax policy rules (hot-reload)
    pairStateMu sync.Mutex
    pairStates  map[string]*FaxPairState        // write-through cache of fax_pair_states
}
```

Endpoint storage is split by scope:
- `TenantEndpoints[tenantID]` — applies to all numbers of a tenant.
- `NumberEndpoints[numberString]` — overrides tenant scope for a specific number.
- `UpstreamFsGateways` — populated from `Endpoints` of `type=global, endpoint_type=gateway`. Used as the fan-out fallback when no tenant/number endpoint matches.

---

## T.38 / Fax Policy Engine

T.38, ECM and V.17 policy is resolved per call by the Postgres-backed fax
policy engine (`gofaxserver/faxpolicy.go`). It replaces the old FreeSWITCH
`mod_db` softmodem fallback and the in-memory flip-flop map.

### Policy rules (`fax_policy_rules`)

Composable, single-purpose rules:

| Field | Values | Notes |
|-------|--------|-------|
| scope | `dst` / `src` / `pair` | Which number(s) the rule matches |
| effect | `t38_off` / `t38_on` / `ecm_off` / `ecm_on` / `v17_off` / `softmodem_only` | One attribute per rule |
| applies_to | `both` / `softmodem` / `bridge` | Softmodem (txfax/rxfax) vs bridged (transcoded) calls are controlled independently |
| origin | `manual` / `auto` | Auto rules are learned from failures |
| expires_at | timestamp / NULL | Auto rules expire for re-probing; manual rules may be temporary |

Resolution collects **all** matching enabled, unexpired rules into an applied
set; per attribute the most specific scope wins (`pair` > `dst` > `src`),
`manual` beats `auto`, and at equal specificity "off" beats "on" (fail-safe).
Auto `dst` rules additionally match on the src side: they encode a learned
capability of a remote number, so an inbound rxfax also refuses a known-bad
far end's T.38 re-INVITE.

### Flip-flop probing (`fax_pair_states`)

The old in-memory `t38PairState` map is now persisted per src→dst pair **and
call type** (`softmodem`/`bridge`). Flip-flop alternation only runs when no
rule decided T.38 — it is purely the probing strategy, mainly for bridged
(transcoded) calls where no fax result telemetry exists.

### Auto-learning (softmodem path only)

Failures with a qualifying signature (`NegotiateCount > 1`, bad rows, or a
T.38 refusal) escalate per remote number:
1. `t38_failure_threshold` (default 1) → auto `t38_off` (applies to **both**
   call types — a destination known not to support T.38 is never offered it,
   including bridged re-INVITEs),
2. `ecm_failure_threshold` (default 2) → auto `ecm_off`,
3. `v17_failure_threshold` (default 3) → auto `v17_off`.

Successes heal: after `recovery_successes` (default 3) consecutive successes
the auto rules are cleared and T.38 is re-probed. Bridged calls get limited
auto-learning: a SIP-level negotiation failure (488/606 etc.) while T.38 was
enabled creates a bridge-scoped `t38_off` rule. Within a single job's retry
chain, a T.38 failure signature forces T.38 off for subsequent attempts
(`retry_chain_escalation`).

Thresholds live in `faxing.policy` in config.json.

The actual decision flow:
- Outbound (`freeswitch_outbound.go`): resolve policy → retry-chain escalation →
  hard G.711 constraint for non-upstream gateways → flip-flop probing if no
  rule decided → apply ECM/V.17 overrides → learn from the result.
- Inbound (`freeswitch_inbound.go`): same resolver, `softmodem` type for
  rxfax and `bridge` type for transcoded calls.

---

## Web API Routes

Defined in `gofaxserver/web.go:19-66`:

```
/admin/*  - Basic auth against web.api_key
  GET    /reload                - Hot-reload config from DB
  GET    /faxes                 - List active fax jobs
  GET    /tenants               - List tenants (numbers preloaded)
  GET    /numbers               - List tenant numbers (optional ?tenant_id=)
  GET    /users                 - List tenant users, redacted (optional ?tenant_id=)
  GET    /endpoints             - List endpoints (optional ?type=&type_id=)
  POST   /tenant                - Create tenant
  PUT    /tenant/{id}           - Update tenant
  DELETE /tenant/{id}           - Delete tenant
  POST   /number                - Add tenant number
  PUT    /number/{id}           - Update tenant number
  DELETE /number?number=&tenant_id=  - Delete tenant number
  POST   /endpoint              - Add endpoint (type: tenant, number, or global)
  PUT    /endpoint/{id}         - Update endpoint
  DELETE /endpoint/{id}         - Delete endpoint
  POST   /user                  - Create tenant user
  PUT    /user/{id}             - Update tenant user
  DELETE /user/{id}             - Delete tenant user
  POST   /fallback              - DEPRECATED shim: creates a manual dst t38_off fax policy rule
  GET    /fax-policies          - List fax policy rules (?number=&scope=&effect=&origin=&applies_to=)
  POST   /fax-policies          - Create a manual fax policy rule
  PUT    /fax-policies/{id}     - Update a rule (origin is immutable)
  DELETE /fax-policies/{id}     - Delete a rule
  POST   /fax-policies/{id}/expire - Expire a rule now (reset to probing)
  GET    /fax-policies/resolve  - Dry-run policy resolution (?src=&dst=&type=softmodem|bridge)
  GET    /fax-policies/pair-states  - List persisted flip-flop pair state (?number=)
  DELETE /fax-policies/pair-states/{id} - Clear a pair state

/tenant/* - No middleware; handler validates Basic Auth
  POST   /user/authenticate     - Authenticate a tenant user; returns api_key, user_id, tenant_id

/fax/*    - Tenant user auth (Basic Auth)
  POST   /send                  - Send fax (multipart/form-data: file, caller_number, callee_number)
  GET    /status                - Check fax status by UUID

/health   - 200 OK liveness probe
```

---

## Database Schema

GORM models in `gofaxserver/`. Auto-migrated on startup (`gofaxserver/db.go:migrateSchema`).

### `tenants`

| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| name | string | Tenant name |
| notify | string | Tenant-level notify string (comma-separated `type->dest` entries) |

### `tenant_numbers`

| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| tenant_id | uint | Foreign key to `tenants` |
| number | string | Phone number (unique) |
| name | string | Caller ID name |
| header | string | Fax header displayed at top |
| notify | string | Per-number notify (overrides tenant-level when non-empty) |

### `endpoints`

| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| type | string | `tenant`, `number`, or `global` |
| type_id | uint | Tenant ID, TenantNumber ID, or 0 (global) |
| endpoint_type | string | `gateway`, `webhook`, or `email` |
| endpoint | string | `xml_name:publicIP` for gateway; URL for webhook; email addr for email |
| priority | uint | Lower = higher priority; `666` = no inbound delivery; `999` = upstream fallback |
| bridge | bool | Enable T.38/G.711 transcoding |

### `tenant_users`

| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| tenant_id | uint | Foreign key to `tenants` |
| username | string | Unique login |
| password | string | Encrypted with `psk` (AES-256) |
| api_key | string | User's API key (caller-supplied on create) |

### `fax_job_results`

A denormalized record of every fax attempt — one row per attempt, written from `queue.go:storeQueueFaxResult` via `QueueFaxResult`. Key fields:

- `job_uuid`, `call_uuid` (correlation)
- `result_type` — `reception`, `bridge`, `transmission`, or `delivery` (webhook/email)
- `attempt_number`, `endpoint_id`, `endpoint_type` — which endpoint and which retry
- `start_ts`, `end_ts`, `hangup_cause`, `transferred_pages`, `success`, `result_text`, `t38_status`, `v17_disabled` — outcome
- `is_bridge`, `bridge_direction`, `bridge_gateway`, `bridge_t38` — bridge metadata
- `used_t38`, `softmodem_fallback` — T.38 decision tracking
- `applied_policies` — JSON array of fax policy rule IDs that applied to this call

`Endpoints` and `SourceInfo` are stored as JSON strings.

### `gateway_templates`

Database-backed FreeSWITCH gateway XML templates (Go `text/template` syntax).
Seeded on first start with `sbc` and `pbx`. See [GATEWAYS.md](GATEWAYS.md#api-driven-provisioning-portal).

### `gateway_configs`

Provisioned gateway records: `name` (== XML filename == endpoint prefix),
`template_id`, `params` (JSON, encrypted at rest with `psk`), `endpoint_id`
(linked endpoint — protected from direct edits), and registration monitor
state (`last_state`, `last_state_checked_at`).

### `dialplan_rules`

Number transformation rules for `dialplan.source = "db"` mode: `position`
(order), `pattern`, `replacement`, `enabled`, `description`. Seeded from the
config rules (or built-in defaults) on first run; hot-reloaded on every write
and on `/admin/reload`.

### `fax_policy_rules`

Fax policy rules for the T.38/ECM/V.17 engine: `scope` (`dst`/`src`/`pair`),
`src_number`, `dst_number`, `effect` (`t38_off`/`t38_on`/`ecm_off`/`ecm_on`/
`v17_off`/`softmodem_only`), `applies_to` (`both`/`softmodem`/`bridge`),
`origin` (`manual`/`auto`), `enabled`, `expires_at`, learning stats
(`failure_count`, `success_count`, `last_seen_at`, `last_t38_status`), `notes`.
Hot-reloaded on every write and on `/admin/reload`.

### `fax_pair_states`

Persisted flip-flop probing state per src→dst pair and call type:
`src_number`, `dst_number`, `call_type` (`softmodem`/`bridge`),
`last_used_t38`, `last_seen`. Unique on (src, dst, call_type).

---

## Configuration

`/etc/gofaxserver/config.json` — full schema in `gofaxlib/config.go`. The most-relevant sections:

| Section | Key | Purpose |
|---------|-----|---------|
| `freeswitch` | `event_client_socket` | Outbound ESL connect target (default `:8021`) |
| | `event_client_socket_password` | ESL password (must match FreeSWITCH `event_socket.conf.xml`) |
| | `event_server_socket` | Inbound ESL listen address (default `:8022`) |
| | `ident`, `header` | TSI / header for rxfax |
| | `softmodem_fallback` | **Deprecated, unused** — fax policy is Postgres-backed (`faxing.policy`) |
| `faxing` | `enable_t38`, `request_t38` | Default T.38 posture |
| | `answer_after` (ms), `wait_time` (ms) | Pre-answer / post-answer timing |
| | `disable_v17_after_retry`, `disable_ecm_after_retry` | Auto-degrade thresholds within a job's retry chain (`"0"` = off) |
| | `failed_response` | Hangup causes that mark a fax as a non-retryable failure |
| | `retry_delay`, `retry_attempts` | Queue retry tuning (delay accepts `s/m/h/d` suffix) |
| | `policy` | Fax policy engine: `enabled`, `learn_enabled`, `t38_failure_threshold` (1), `ecm_failure_threshold` (2), `v17_failure_threshold` (3), `recovery_successes` (3), `auto_rule_ttl` (720h), `pair_state_ttl` (15m), `retry_chain_escalation`, `bridge_learn` |
| `database` | host/port/user/password/database | PostgreSQL DSN source |
| `web` | `listen`, `api_key` | Iris listen address and admin API key |
| `loki` | `push_url`, `enabled`, `username`, `password`, `job` | Optional Loki log shipping (set `enabled: true` to push) |
| `smtp` | `host`, `port`, `username`, `password`, `encryption`, `from_address`, `from_name` | SMTP for `email_*` notifications |
| `psk` | string | Symmetric key for tenant-user password encryption |

PostgreSQL SSL mode and timezone are environment-variable overrides (`POSTGRES_SSLMODE`, `POSTGRES_TIMEZONE`) — see `gofaxserver/db.go:getPostgresDSN`.

---

## Related Documentation

- [INSTALLATION.md](INSTALLATION.md) — Full installation guide
- [SETUP.md](SETUP.md) — Step-by-step tenant setup procedure
- [TENANTS.md](TENANTS.md) — Tenant, endpoint, and notify configuration
- [API_REFERENCE.md](API_REFERENCE.md) — Full HTTP API documentation
- [GATEWAYS.md](GATEWAYS.md) — FreeSWITCH gateway configuration
- [`../BACKUP.md`](../BACKUP.md) — Backup and restore procedures
- [`../gofaxserver.excalidraw`](../gofaxserver.excalidraw) — Visual architecture diagram
