# gofaxserver

A modern, multi-tenant Fax over IP server using FreeSWITCH and SpanDSP. Unlike legacy fax solutions, gofaxserver directly interfaces with FreeSWITCH via Event Socket, eliminating the need for external fax emulation or legacy utilities.

## Features

- **Multi-tenant Architecture** - Isolated tenant management with per-tenant numbers, endpoints, and users
- **SIP Connectivity** - Connect to PBXes, SBCs, and SIP providers with or without registration
- **T.38 + G.711 Support** - Full T.38 protocol with intelligent flip-flop fallback to G.711 audio
- **Fax Bridging** - Transcode faxes between T.38 and audio endpoints
- **Priority-based Routing** - Failover using multiple gateways with configurable priorities
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
│        fax_job_results — GORM auto-migrated on startup)              │
└──────────────────────────────────────────────────────────────────────┘
       │              │                                  │
       ▼              ▼                                  ▼
  FreeSWITCH    FreeSWITCH (Upstream)                HTTP Clients
  (customer PBX) (carrier / SBC)              (Tenants / Admin)
  (mod_spandsp)  mod_spandsp
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design and [gofaxserver.excalidraw](gofaxserver.excalidraw) for a visual diagram.

> **Fax Portal:** a separate multi-tenant web UI (`portal/`, own binary + DB) that lets end users send faxes and track jobs through this API — see [docs/PORTAL.md](docs/PORTAL.md).

### Core Components

| Component | File | Description |
|-----------|------|-------------|
| **Event Socket Server** | `gofaxserver/freeswitch_inbound.go`, `freeswitch_outbound.go` | Inbound (`:8022`) and outbound (`:8021`) ESL connections to FreeSWITCH |
| **Router** | `gofaxserver/router.go` | Routes incoming calls based on number-to-tenant mapping; honors upstream gateway set |
| **Dialplan Manager** | `gofaxserver/dialplan.go`, `server.go:loadDialplan` | Applies regex transformations to caller/callee numbers before routing; rules configurable via `dialplan` in `config.json` |
| **Queue** | `gofaxserver/queue.go` | Manages outbound fax jobs with priority-based endpoint selection and retry |
| **Web Server** | `gofaxserver/web.go` | Iris-based REST API on `:8080` (or `web.listen`) |
| **FaxTracker** | `gofaxserver/faxtracker.go` | Real-time tracking of in-flight fax jobs |
| **LogManager** | `gofaxlib/log.go` | Dispatches structured logs to stdout and Loki |

### T.38 Negotiation Strategy

gofaxserver implements an intelligent T.38 flip-flop mechanism:

1. **Upstream Gateways Only** - T.38 is only enabled for calls to/from upstream (carrier) gateways
2. **Flip-Flop Retry** - On first call to a number pair, T.38 is allowed; on a retry within TTL (15 min), it is flipped
3. **Softmodem Fallback** - Numbers with repeated T.38 failures are automatically switched to G.711 (stored in FreeSWITCH `mod_db`)
4. **Local Endpoints** - Calls to/from tenant gateways always use G.711 (no T.38)

## Installation

### Prerequisites

- Debian 12 (bookworm) recommended
- Go 1.22+ (only required when building from source)
- PostgreSQL 12+
- FreeSWITCH with `mod_spandsp`, `mod_sofia`, `mod_event_socket`
- ImageMagick 7+ and Ghostscript (for PDF → TIFF conversion of outgoing faxes)

### FreeSWITCH Packages

```bash
TOKEN=YOURSIGNALWIRETOKEN

apt-get update && apt-get install -y gnupg2 wget lsb-release
wget --http-user=signalwire --http-password=$TOKEN -O /usr/share/keyrings/signalwire-freeswitch-repo.gpg \
    https://freeswitch.signalwire.com/repo/deb/debian-release/signalwire-freeswitch-repo.gpg

echo "machine freeswitch.signalwire.com login signalwire password $TOKEN" > /etc/apt/auth.conf
echo "deb [signed-by=/usr/share/keyrings/signalwire-freeswitch-repo.gpg] https://freeswitch.signalwire.com/repo/deb/debian-release/ $(lsb_release -sc) main" > /etc/apt/sources.list.d/freeswitch.list

apt-get update
apt-get install freeswitch freeswitch-mod-commands freeswitch-mod-dptools \
    freeswitch-mod-event-socket freeswitch-mod-sofia freeswitch-mod-spandsp \
    freeswitch-mod-tone-stream freeswitch-mod-db freeswitch-mod-syslog freeswitch-mod-logfile
```

The included `examples/freeswitch/` directory contains a complete set of autoload configs (Sofia `fax` profile, SpanDSP, etc.) and gateway templates — copy them into `/etc/freeswitch/` and adjust the realm/IP for your environment.

### Database Setup

```sql
CREATE DATABASE gofaxserver;
CREATE USER gofaxserver WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE gofaxserver TO gofaxserver;
-- Required for GORM auto-migration on first startup:
GRANT ALL ON SCHEMA public TO gofaxserver;
```

The server auto-migrates the schema (tenants, tenant_numbers, endpoints, tenant_users, fax_job_results) on startup — no manual migration step is required.

## Configuration

Configuration is stored in `/etc/gofaxserver/config.json`. A complete example (matching `gofaxlib/config.go`):

```json
{
  "freeswitch": {
    "event_client_socket": "127.0.0.1:8021",
    "event_client_socket_password": "ClueCon",
    "event_server_socket": "127.0.0.1:8022",
    "ident": "gofaxserver",
    "header": "Fax Server",
    "verbose": false,
    "softmodem_fallback": true
  },
  "faxing": {
    "temp_dir": "/tmp",
    "enable_t38": true,
    "request_t38": true,
    "recipient_from_diversion_header": false,
    "answer_after": 2000,
    "wait_time": 1000,
    "disable_v17_after_retry": "0",
    "disable_ecm_after_retry": "0",
    "failed_response": ["UNALLOCATED_NUMBER", "CALL_REJECTED"],
    "retry_delay": "60s",
    "retry_attempts": "3"
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
  "psk": "change-me-32-bytes-of-entropy"
}
```

Notes:
- `event_server_socket` (port **8022**) is the inbound ESL listener for FreeSWITCH to push events to gofaxserver. The Sofia `fax` profile points its dialplan at `socket:127.0.0.1:8022 async full` (see `examples/freeswitch/autoload_configs/sofia.conf.xml`).
- `event_client_socket` (port **8021**) is the outbound ESL connection gofaxserver uses to originate calls and write to `mod_db`.
- `answer_after` and `wait_time` are in **milliseconds** (`uint64`).
- `retry_delay` accepts `s/m/h/d` suffixes (e.g. `60s`, `5m`).
- `psk` is the symmetric key used to encrypt/decrypt tenant user passwords — generate a strong random value in production.
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
| POST | `/admin/fallback` | Set softmodem fallback for a number |

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

`notify` on the number overrides the tenant-level `notify` for faxes involving that number.

### Endpoint
```json
{
  "id": 1,
  "type": "tenant",           // "tenant", "number", or "global"
  "type_id": 1,               // Tenant ID (tenant), TenantNumber.ID (number), or 0 (global)
  "endpoint_type": "gateway", // "gateway", "webhook", "email"
  "endpoint": "carrier_gw:203.0.113.5",  // gateway: `xml_name:publicIP`; webhook: URL; email: addr[;addr2]
  "priority": 0,              // Lower = higher priority; 666 = no inbound delivery; 999 = upstream fallback
  "bridge": false             // Enable T.38/G.711 transcoding
}
```

Global endpoints (`type=global`, `type_id=0`) are the upstream carrier gateways. They are loaded into `UpstreamFsGateways` and used as a fan-out fallback when no tenant/number endpoint matches. They can now be created via the API.

## Softmodem Fallback

When T.38 negotiation fails repeatedly with a remote station, gofaxserver can be told to disable T.38 for a specific number. The flag is stored in FreeSWITCH's `mod_db` under the `fallback` realm.

Enable the feature with `freeswitch.softmodem_fallback: true` in configuration. The flag is also automatically applied per call (flip-flop + per-side match) — see [docs/TENANTS.md](docs/TENANTS.md).

To manually set fallback for a number:

```bash
curl -X POST http://localhost:8080/admin/fallback \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"number": "5551234567"}'
```

## Building from Source

The entry point is `gofaxserver/cmd/gofaxserver/main.go`. The module path is `gofaxserver` (local), so the simplest build is:

```bash
go build -o gofaxserver ./gofaxserver/cmd/gofaxserver
```

Run with `-c /etc/gofaxserver/config.json` (default if not specified) and `-version` to print the build version.

### Build Docker image

```bash
./build.sh           # uses the project-root Dockerfile
# or:
docker build -t gofaxserver:latest .
```

The Docker image runs the server as the non-root `appuser` and exposes port 8080 (web) and 8022 (inbound ESL). FreeSWITCH is **not** included in the image — run it on the host or as a separate container and mount `/etc/gofaxserver/config.json` and a writable `temp_dir`.

### Debian package

A `debian/` directory is provided to build a `.deb` via `dpkg-buildpackage`. See [debian/](debian/) for the manifest and the systemd unit.

## License

GNU General Public License v2.0
