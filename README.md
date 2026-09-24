# gofaxserver

A modern, multi-tenant Fax over IP server using FreeSWITCH and SpanDSP. Unlike legacy fax solutions, gofaxserver directly interfaces with FreeSWITCH via Event Socket, eliminating the need for external fax emulation or legacy utilities.

## Features

- **Multi-tenant Architecture** - Isolated tenant management with per-tenant numbers, endpoints, and users
- **SIP Connectivity** - Connect to PBXes, SBCs, and SIP providers with or without registration
- **T.38 + G.711 Support** - Full T.38 protocol with policy-driven fallback to G.711 audio
- **Fax Policy Engine** - Postgres-backed T.38/ECM/V.17 rules with adaptive auto-learning, per-call-type scoping (softmodem vs bridged), and persisted flip-flop probing — replaces the old FreeSWITCH `mod_db` fallback
- **Fax Bridging** - Transcode faxes between T.38 and audio endpoints
- **Priority-based Routing** - Failover using multiple gateways with configurable priorities
- **API-driven Gateway Provisioning** - Create/update/delete FreeSWITCH gateway XML from the REST API or portal, with DB-backed editable templates and automatic ESL reload
- **Configurable Dialplan** - Number transformation rules from `config.json` or hot-reloadable from the database
- **Registration Monitor** - Polls Sofia gateway registration state, logs transitions, and exposes health in the portal
- **RESTful API** - Complete API for tenant administration and fax operations
- **Comprehensive Logging** - Loki and PostgreSQL integration for detailed audit trails
- **Flexible Notifications** - Email, webhooks, and customizable notification methods

## Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                          gofaxserver                                 │
├──────────────┬────────────┬─────────────┬────────────┬───────────────┤
│ Event Socket │  Router    │  Queue      │  Web API   │  FaxTracker / │
│   Server     │            │             │  (Iris)    │  LogManager   │
│ (in+out)     │  + Dialplan│  (priority, │            │  /Dialplan    │
│              │  Manager   │   retry)    │            │               │
├──────────────┴────────────┴─────────────┴────────────┴───────────────┤
│                            PostgreSQL                                │
│       (tenants, tenant_numbers, endpoints, tenant_users,            │
│        fax_job_results, gateway_templates, gateway_configs,         │
│        dialplan_rules, fax_policy_rules, fax_pair_states —          │
│        GORM auto-migrated on startup)                               │
└──────────────────────────────────────────────────────────────────────┘
       │              │                                  │
       ▼              ▼                                  ▼
  FreeSWITCH    FreeSWITCH (Upstream)                HTTP Clients
  (customer PBX) (carrier / SBC)              (Tenants / Admin)
  (mod_spandsp)  mod_spandsp
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design and [gofaxserver.excalidraw](gofaxserver.excalidraw) for a visual diagram.

> **Fax Portal:** a separate multi-tenant web UI (`portal/`, own binary + DB) that lets end users send faxes, track jobs with instant pushed status updates, and receive inbound faxes into an encrypted-at-rest inbox — see [docs/PORTAL.md](docs/PORTAL.md). A Caddy reverse proxy (part of the main compose stack) fronts the portal **and** the gofaxserver API on one address — plain HTTP by default, automatic HTTPS when you set a DNS name in the `Caddyfile`; existing direct API clients keep working unchanged, through the proxy or direct `:8080`.

### Core Components

| Component | File | Description |
|-----------|------|-------------|
| **Event Socket Server** | `gofaxserver/freeswitch_inbound.go`, `freeswitch_outbound.go` | Inbound (`:8022`) and outbound (`:8021`) ESL connections to FreeSWITCH |
| **Router** | `gofaxserver/router.go` | Routes incoming calls based on number-to-tenant mapping; honors upstream gateway set |
| **Dialplan Manager** | `gofaxserver/dialplan.go`, `server.go:loadDialplan` | Applies regex transformations to caller/callee numbers before routing; rules come from `dialplan` in `config.json` (`source: "config"`) or the `dialplan_rules` table (`source: "db"`, hot-reloadable) |
| **Gateway Provisioning** | `gofaxserver/gateway_provision.go`, `web_gateways.go` | Renders DB-backed templates into `gateway_config_dir`, tracks gateways in `gateway_configs`, reloads FreeSWITCH via ESL |
| **Gateway Monitor** | `gofaxserver/gateway_monitor.go` | Polls Sofia registration state, logs transitions, persists `last_state` |
| **Queue** | `gofaxserver/queue.go` | Manages outbound fax jobs with priority-based endpoint selection and retry |
| **Web Server** | `gofaxserver/web.go` | Iris-based REST API on `:8080` (or `web.listen`) |
| **FaxTracker** | `gofaxserver/faxtracker.go` | Real-time tracking of in-flight fax jobs |
| **Fax Policy Engine** | `gofaxserver/faxpolicy.go`, `web_faxpolicy.go` | Postgres-backed T.38/ECM/V.17 policy rules, adaptive failure learning, and persisted flip-flop pair state |
| **LogManager** | `gofaxlib/log.go` | Dispatches structured logs to stdout and Loki |

### T.38 Negotiation Strategy

T.38 decisions are driven by the Postgres-backed **fax policy engine** (`faxing.policy` in config):

1. **Upstream Gateways Only** - T.38 is only enabled for calls to/from upstream (carrier) gateways
2. **Policy Rules** - Composable `fax_policy_rules` (T.38/ECM/V.17 on/off, softmodem-only) scoped by destination, source, or src/dst pair, and by call type (`softmodem` vs `bridge`) — see the [Fax Policy](#fax-policy) section
3. **Adaptive Learning** - Repeated T.38 negotiation failures automatically escalate `t38_off` → `ecm_off` → `v17_off` rules; consecutive successes (or TTL expiry) heal them
4. **Flip-Flop Probing** - Where no rule decides, a per-pair, per-call-type state (persisted in `fax_pair_states`) flips T.38 on retry within its TTL — mainly for telemetry-blind bridged calls
5. **Local Endpoints** - Calls to/from tenant gateways always use G.711 (no T.38)

## Installation

**Full guide: [docs/INSTALLATION.md](docs/INSTALLATION.md)** — requirements, port matrix, deployment paths (all-container or FreeSWITCH on the host), portal + TLS, post-install verification, hardening, and troubleshooting.

Quick start (all containers on a Docker host — FreeSWITCH included):

```bash
git clone <repo-url> gofaxserver && cd gofaxserver
make setup    # seeds .env, config.json, volumes/ — never overwrites existing files
$EDITOR .env config.json                          # DB creds must match; set SIGNALWIRE_TOKEN, api_key, psk
$EDITOR volumes/freeswitch/vars.xml               # REQUIRED: sofia_ip = host LAN IP
make fs-build # needs SIGNALWIRE_TOKEN (from .env or the environment)
make up       # auto-builds the gofaxserver + portal images if missing
```

No `make`? The equivalent manual steps are in [docs/INSTALLATION.md](docs/INSTALLATION.md).

To run FreeSWITCH as a Debian host service instead (with gofaxserver/PostgreSQL in Docker via `docker-compose.yml`, or bare-metal from the `debian/` package), see [Path B](docs/INSTALLATION.md#path-b--freeswitch-as-a-debian-host-service). The fax portal deploys on top of either path — see [docs/PORTAL.md](docs/PORTAL.md).

## Configuration

Configuration is stored in `/etc/gofaxserver/config.json` (`./config.json` in the all-container compose). The server **auto-migrates the database schema** (tenants, tenant_numbers, endpoints, tenant_users, fax_job_results, gateway_templates, gateway_configs, dialplan_rules, fax_policy_rules, fax_pair_states) on startup — no manual migration step is required. A complete example (matching `gofaxlib/config.go`):

```json
{
  "freeswitch": {
    "event_client_socket": "127.0.0.1:8021",
    "event_client_socket_password": "ClueCon",
    "event_server_socket": "127.0.0.1:8022",
    "ident": "gofaxserver",
    "header": "Fax Server",
    "verbose": false,
    "gateway_config_dir": "/etc/freeswitch/gateways",
    "gateway_config_chown": "freeswitch:freeswitch",
    "gateway_profile": "fax",
    "gateway_monitor_seconds": 60
  },
  "faxing": {
    "temp_dir": "/var/lib/gofaxserver/tmp",
    "temp_max_age": "24h",
    "enable_t38": true,
    "request_t38": true,
    "recipient_from_diversion_header": false,
    "answer_after": 2000,
    "wait_time": 1000,
    "disable_v17_after_retry": "0",
    "disable_ecm_after_retry": "0",
    "failed_response": ["UNALLOCATED_NUMBER", "CALL_REJECTED"],
    "retry_delay": "60s",
    "retry_attempts": "3",
    "policy": {
      "enabled": true,
      "learn_enabled": true,
      "t38_failure_threshold": 1,
      "ecm_failure_threshold": 2,
      "v17_failure_threshold": 3,
      "recovery_successes": 3,
      "auto_rule_ttl": "720h",
      "pair_state_ttl": "15m",
      "retry_chain_escalation": true,
      "bridge_learn": true
    }
  },
  "database": {
    "host": "localhost",
    "port": "5432",
    "user": "gofaxserver",
    "password": "your_password",
    "database": "gofaxserver"
  },
  "web": {
    "listen": ":8080",
    "api_key": "your_api_key"
  },
  "portal": {
    "url": "http://127.0.0.1:8081",
    "api_key": ""
  },
  "loki": {
    "push_url": "http://localhost:3100/loki/api/v1/push",
    "username": "",
    "password": "",
    "job": "faxserver",
    "enabled": true
  },
  "smtp": {
    "host": "smtp.example.com",
    "port": 587,
    "username": "smtpuser",
    "password": "smtppassword",
    "encryption": "tls",
    "from_address": "noreply@example.com",
    "from_name": "Fax Server"
  },
  "dialplan": {
    "source": "config",
    "rules": [
      { "pattern": "^1(\\d{10}).*$", "replacement": "$1" },
      { "pattern": "^(\\d{10}).*$", "replacement": "$1" }
    ]
  },
  "psk": "change-me-32-bytes-of-entropy"
}
```

Notes:
- `event_server_socket` (port **8022**) is the inbound ESL listener for FreeSWITCH to push events to gofaxserver. The Sofia `fax` profile points its dialplan at `socket:127.0.0.1:8022 async full` (see `examples/freeswitch/autoload_configs/sofia.conf.xml`).
- `event_client_socket` (port **8021**) is the outbound ESL connection gofaxserver uses to originate calls.
- `answer_after` and `wait_time` are in **milliseconds** (`uint64`).
- `temp_dir` holds store-and-forward fax content (uploaded TIFFs, received faxes) — **FreeSWITCH must see the same directory at the same path**, so in container setups it is a shared mount (the compose files handle this). A built-in janitor deletes orphaned files older than `temp_max_age` (default `24h`, `"0s"` disables); completed jobs delete their files immediately.
- `retry_delay` accepts `s/m/h/d` suffixes (e.g. `60s`, `5m`).
- `psk` is the symmetric key used to encrypt/decrypt tenant user passwords and gateway credentials — generate a strong random value in production.
- `gateway_config_dir` enables API-driven provisioning: rendered gateway XML is written there and FreeSWITCH is reloaded over ESL. Leave empty to disable. `gateway_config_chown` optionally chowns files, `gateway_profile` selects the Sofia profile to rescan (default `fax`), `gateway_monitor_seconds` is the registration poll interval and the re-resolution cadence for hostname-valued gateway ACL entries (default 60, negative disables both). See [docs/GATEWAYS.md](docs/GATEWAYS.md).
- `dialplan.source` is `"config"` (rules from this file, shown above) or `"db"` (rules from the `dialplan_rules` table, editable via `/admin/dialplan*` and hot-reloaded by `/admin/reload`). Omit the whole `dialplan` section to use the built-in defaults.
- `faxing.policy` configures the [fax policy engine](#fax-policy) (T.38/ECM/V.17 rules + adaptive learning). The whole section is optional — sensible defaults apply when absent. The old `freeswitch.softmodem_fallback` key is deprecated and ignored.
- PostgreSQL SSL mode and timezone can be overridden via `POSTGRES_SSLMODE` and `POSTGRES_TIMEZONE` environment variables (defaults: `disable`, `America/Vancouver`).

## API Reference

See [docs/API_REFERENCE.md](docs/API_REFERENCE.md) for the full reference. Quick reference:

### Authentication

| API Group | Auth Method | Credentials |
|-----------|-------------|-------------|
| Admin API (`/admin/*`) | Basic Auth | `username:<API_KEY>` (api_key from `web.api_key`) |
| Tenant User API (`/fax/*`) | Basic Auth | `<username>:<password>` (tenant user credentials) |
| Self-service (`/tenant/user/authenticate`) | Basic Auth | `<username>:<password>` — returns user/tenant id and api_key |

### Admin Endpoints (`/admin`)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/admin/reload` | Hot-reload configuration from database |
| GET | `/admin/faxes` | List active fax jobs with real-time tracking |
| GET | `/admin/tenants` | List all tenants (numbers preloaded) |
| GET | `/admin/numbers?tenant_id=` | List tenant numbers (optional filter) |
| GET | `/admin/users?tenant_id=` | List tenant users, passwords redacted (optional filter) |
| GET | `/admin/endpoints?type=&type_id=` | List endpoints (optional filters) |
| POST | `/admin/tenant` | Create a new tenant |
| PUT | `/admin/tenant/{id}` | Update tenant |
| DELETE | `/admin/tenant/{id}` | Delete tenant |
| POST | `/admin/number` | Add phone number to tenant (supports per-number `notify`) |
| PUT | `/admin/number/{id}` | Update tenant number |
| DELETE | `/admin/number?number=...&tenant_id=...` | Delete tenant number |
| POST | `/admin/endpoint` | Add routing endpoint (type: `tenant`, `number`, or `global`) |
| PUT | `/admin/endpoint/{id}` | Update endpoint |
| DELETE | `/admin/endpoint/{id}` | Delete endpoint |
| POST | `/admin/user` | Create tenant user |
| PUT | `/admin/user/{id}` | Update tenant user (password re-encrypted if supplied) |
| DELETE | `/admin/user/{id}` | Delete tenant user |
| POST | `/admin/fallback` | **Deprecated** — creates a manual dst `t38_off` fax policy rule |
| GET/POST | `/admin/fax-policies` | List / create fax policy rules |
| PUT/DELETE | `/admin/fax-policies/{id}` | Update / delete a fax policy rule |
| POST | `/admin/fax-policies/{id}/expire` | Expire a rule (reset to probing) |
| GET | `/admin/fax-policies/resolve?src=&dst=&type=` | Dry-run policy resolution for a src/dst/call-type |
| GET | `/admin/fax-policies/pair-states` | List flip-flop pair states |
| DELETE | `/admin/fax-policies/pair-states/{id}` | Clear a pair state |
| GET | `/admin/gateways` | List provisioned gateways + unmanaged XML files found on disk |
| POST | `/admin/gateway` | Provision a gateway (renders template, writes XML, ESL reload, optional endpoint) |
| PUT | `/admin/gateway/{name}` | Update a provisioned gateway |
| DELETE | `/admin/gateway/{name}` | Deprovision a gateway (removes XML + DB record) |
| POST | `/admin/gateway/{name}/repair` | Re-render XML from DB state |
| POST | `/admin/gateways/adopt` | Adopt an unmanaged on-disk XML into DB management |
| DELETE | `/admin/gateways/unmanaged/{name}` | Delete an unmanaged on-disk XML |
| GET/POST | `/admin/gateway/templates` | List / create gateway templates |
| PUT/DELETE | `/admin/gateway/templates/{id}` | Update / delete a gateway template |
| GET | `/admin/dialplan` | Show dialplan source and rules |
| POST | `/admin/dialplan/rules` | Create a dialplan rule |
| PUT/DELETE | `/admin/dialplan/rules/{id}` | Update / delete a dialplan rule |
| POST | `/admin/dialplan/rules/reorder` | Reorder dialplan rules |

### Fax Endpoints (`/fax`)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/fax/send` | Send a fax (multipart/form-data) — tenant user auth required |
| GET | `/fax/status?uuid=...` | Get fax job status and logs — tenant user auth required |

### Example: Send a Fax

```bash
curl -X POST http://localhost:8080/fax/send \
  -H "Authorization: Basic $(echo -n 'user:password' | base64)" \
  -F "file=@document.pdf" \
  -F "caller_number=5551234567" \
  -F "callee_number=5559876543"
```

### Example: Check Active Faxes

```bash
curl http://localhost:8080/admin/faxes \
  -H "Authorization: Basic $(echo -n 'admin:api_key' | base64)"
```

Response:
```json
{
  "active": 2,
  "items": [
    {
      "job_uuid": "f0c648ff-702a-43e9-b8a3-d0ebffe70cd9",
      "phase": "BRIDGING",
      "caller": "2364224783",
      "callee": "6042339777",
      "endpoint_type": "gateway",
      "endpoint_label": "upstream",
      "endpoint_value": "upstream_carrier",
      "attempt": 1,
      "current_priority": 0,
      "started_at": "2025-12-23T12:50:52Z"
    }
  ]
}
```

## Data Models

### Tenant
```json
{
  "id": 1,
  "name": "Acme Corp",
  "notify": "email_report->support@acme.com,webhook_form->https://n8n.example.com/wh"
}
```

### TenantNumber
```json
{
  "id": 1,
  "tenant_id": 1,
  "number": "5551234567",
  "name": "Main Fax",
  "header": "ACME Corp",
  "notify": "email_full_failure->failures@acme.com"
}
```

`notify` on the number is merged with the tenant-level `notify` for faxes involving that number — both fire, deduplicated by `type->destination`.

### Endpoint
```json
{
  "id": 1,
  "type": "tenant",           // "tenant", "number", or "global"
  "type_id": 1,               // Tenant ID (tenant), TenantNumber.ID (number), or 0 (global)
  "endpoint_type": "gateway", // "gateway", "webhook", "email", "portal"
  "endpoint": "carrier_gw:203.0.113.5",  // gateway: `xml_name:publicIP`; webhook: URL; email: addr[;addr2]; portal: org's svc_username
  "priority": 0,              // Lower = higher priority; 666 = no inbound delivery; 999 = upstream fallback
  "bridge": false             // Enable T.38/G.711 transcoding
}
```

Global endpoints (`type=global`, `type_id=0`) are the upstream carrier gateways. They are loaded into `UpstreamFsGateways` and used as a fan-out fallback when no tenant/number endpoint matches. They can now be created via the API.

Endpoints created through gateway provisioning (`POST /admin/gateway`) are *managed*: they are linked to a `gateway_configs` row and `PUT/DELETE /admin/endpoint/{id}` returns **409 Conflict** for them — modify or remove them via the `/admin/gateway*` routes instead. For `gateway` endpoints, the value is `name:publicIP` where the ACL match is exact on the IP part (see [docs/GATEWAYS.md](docs/GATEWAYS.md)).

## Fax Policy

When T.38 negotiation fails repeatedly with a remote station, gofaxserver can disable T.38 (and, on further failures, ECM/V.17) for that destination, source, or src/dst pair. Policy is stored in Postgres (`fax_policy_rules`) — **no FreeSWITCH `mod_db` involved** — and hot-reloaded on every write. The engine also auto-learns rules from failure signatures and heals them after consecutive successes or TTL expiry (`faxing.policy` thresholds).

Rules are composable and single-purpose: `t38_off`/`t38_on`, `ecm_off`/`ecm_on`, `v17_off`, `softmodem_only`, and `var_override` (inject/override an outbound dialstring channel variable via `var_name`/`var_value` — e.g. `fax_verbose=true` for one problematic destination; this replaces the old mod_db `override-<number>` realm). Each rule is scoped by `dst`, `src`, or `pair`, and by call type via `applies_to` (`both`/`softmodem`/`bridge`) — so you can disable T.38 for softmodem calls to a number while leaving bridged (transcoded) calls untouched. Resolution per attribute: pair > dst > src scope, manual beats auto-learned, and "off" beats "on" (fail-safe); for `var_override`, the most specific rule wins per variable name and different names merge.

The old `POST /admin/fallback` endpoint still works as a deprecated shim — it creates a manual dst-scoped `t38_off` rule:

```bash
curl -X POST http://localhost:8080/admin/fallback \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"number": "5551234567"}'
```

Prefer the full rule API (also exposed in the portal under **Admin → Fax Policies**):

```bash
curl -X POST http://localhost:8080/admin/fax-policies \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"scope": "dst", "dst_number": "5551234567", "effect": "t38_off", "applies_to": "both"}'
```

Full reference: [docs/API_REFERENCE.md](docs/API_REFERENCE.md).

## Building from Source

The entry point is `gofaxserver/cmd/gofaxserver/main.go`. The module path is `gofaxserver` (local), so the simplest build is:

```bash
make build           # -> bin/gofaxserver
# or by hand:
go build -o gofaxserver ./gofaxserver/cmd/gofaxserver
```

Run with `-c /etc/gofaxserver/config.json` (default if not specified) and `-version` to print the build version.

### Build Docker image

```bash
make docker-build         # wraps the project-root Dockerfile (replaces build.sh)
make portal-docker-build  # portal image (frontend + backend, all inside Docker)
# or:
docker build -t gofaxserver:latest .
```

(`make up` also builds both images automatically when they're missing — the explicit targets are for forcing a rebuild after code changes.)

The Docker image runs the server as the non-root `appuser` and exposes port 8080 (web) and 8022 (inbound ESL). FreeSWITCH is **not** included in the image — run it on the host or as a separate container and mount `/etc/gofaxserver/config.json` and a writable `temp_dir`.

To enable API-driven FreeSWITCH gateway provisioning (Admin → Gateways in the portal), also set `freeswitch.gateway_config_dir` in `config.json` and mount that directory so both gofaxserver and FreeSWITCH see the same path (e.g. `/etc/freeswitch/gateways`). See [docs/GATEWAYS.md](docs/GATEWAYS.md#api-driven-provisioning-portal).

### Debian package

A `debian/` directory is provided to build a `.deb` via `dpkg-buildpackage`. See [debian/](debian/) for the manifest and the systemd unit.

## License

GNU General Public License v2.0
