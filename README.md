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
┌─────────────────────────────────────────────────────────────┐
│                         gofaxserver                         │
├───────────────┬─────────────┬──────────────┬───────────────┤
│  Event Socket │   Router    │    Queue     │   Web API     │
│    Server     │             │              │               │
├───────────────┴─────────────┴──────────────┴───────────────┤
│                         PostgreSQL                          │
└─────────────────────────────────────────────────────────────┘
         │                                        │
         ▼                                        ▼
    FreeSWITCH                              HTTP Clients
    (mod_spandsp)                           (Tenants/Admin)
```

### Core Components

| Component | Description |
|-----------|-------------|
| **Event Socket Server** | Handles FreeSWITCH communication for inbound/outbound fax operations |
| **Router** | Routes incoming calls based on number-to-tenant mapping with dialplan transformations |
| **Queue** | Manages outbound fax jobs with retry logic and priority-based endpoint selection |
| **Web Server** | Provides RESTful API for administration and tenant operations |
| **FaxTracker** | Real-time tracking of in-flight fax jobs with phases: `ROUTED`, `BRIDGING`, `RECEIVING`, `SENDING`, `WAITING`, `DONE` |

### T.38 Negotiation Strategy

gofaxserver implements an intelligent T.38 flip-flop mechanism:

1. **Upstream Gateways Only** - T.38 is only enabled for calls to/from upstream (carrier) gateways
2. **Flip-Flop Retry** - On first call to a number pair, T.38 is enabled; on retry within TTL (15 min), it's disabled
3. **Softmodem Fallback** - Numbers with repeated T.38 failures are automatically switched to G.711
4. **Local Endpoints** - Calls to/from tenant gateways always use G.711 (no T.38)

## Installation

### Prerequisites

- Debian 12 (bookworm) recommended
- PostgreSQL 12+
- FreeSWITCH with mod_spandsp

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

### Database Setup

```sql
CREATE DATABASE gofaxserver;
CREATE USER gofaxserver WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE gofaxserver TO gofaxserver;
```

## Configuration

Configuration is stored in `/etc/gofaxserver/config.json`:

```json
{
  "freeswitch": {
    "event_client_socket": "127.0.0.1:8021",
    "event_client_socket_password": "ClueCon",
    "event_server_socket": "127.0.0.1:8084",
    "ident": "gofaxserver",
    "softmodem_fallback": true
  },
  "faxing": {
    "temp_dir": "/tmp",
    "enable_t38": true,
    "request_t38": true,
    "answer_after": 2,
    "retry_delay": "300",
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
    "job": "faxserver"
  }
}
```

## API Reference

### Authentication

| API Group | Auth Method | Credentials |
|-----------|-------------|-------------|
| Admin API | Basic Auth | `username:api_key` (api_key from config) |
| Fax API | Basic Auth | `username:password` (tenant user credentials) |

### Admin Endpoints (`/admin`)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/admin/reload` | Hot-reload configuration from database |
| GET | `/admin/faxes` | List active fax jobs with real-time tracking |
| POST | `/admin/tenant` | Create a new tenant |
| PUT | `/admin/tenant/{id}` | Update tenant |
| DELETE | `/admin/tenant/{id}` | Delete tenant |
| POST | `/admin/number` | Add phone number to tenant |
| PUT | `/admin/number/{id}` | Update tenant number |
| DELETE | `/admin/number?number=...&tenant_id=...` | Delete tenant number |
| POST | `/admin/endpoint` | Add routing endpoint |
| PUT | `/admin/endpoint/{id}` | Update endpoint |
| DELETE | `/admin/endpoint/{id}` | Delete endpoint |
| POST | `/admin/user` | Create tenant user |
| PUT | `/admin/user/{id}` | Update tenant user |
| DELETE | `/admin/user/{id}` | Delete tenant user |
| POST | `/admin/fallback` | Set softmodem fallback for number |

### Fax Endpoints (`/fax`)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/fax/send` | Send a fax (multipart/form-data) |
| GET | `/fax/status?uuid=...` | Get fax job status and logs |

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
      "endpoint_label": "upstream",
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
  "email": "contact@acme.com"
}
```

### TenantNumber
```json
{
  "id": 1,
  "tenant_id": 1,
  "number": "5551234567",
  "notify_emails": "notify@acme.com",
  "cid": "+15551234567",
  "webhook": "https://acme.com/fax-webhook"
}
```

### Endpoint
```json
{
  "id": 1,
  "type": "tenant",           // "tenant", "number", or "global"
  "type_id": 1,               // Tenant ID or TenantNumber ID
  "endpoint_type": "gateway", // "gateway", "webhook", "email"
  "endpoint": "carrier_gw",   // FreeSWITCH gateway name or URL
  "priority": 0,              // Lower = higher priority
  "bridge": false             // Enable fax bridging/transcoding
}
```

## Softmodem Fallback

When T.38 negotiation fails repeatedly with a remote station, gofaxserver can automatically disable T.38 for that number:

1. Failed transmissions with multiple negotiations or bad rows trigger fallback
2. Fallback entries are stored in FreeSWITCH's `mod_db` with realm `fallback/<callerid>/<timestamp>`
3. Enable with `softmodem_fallback: true` in configuration

To manually set fallback for a number:
```bash
curl -X POST http://localhost:8080/admin/fallback \
  -H "Authorization: Basic $(echo -n 'admin:apikey' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"number": "5551234567"}'
```

## Building from Source

```bash
go get github.com/sagostin/gofaxserver/...
```

### Build Debian Package

```bash
apt install git dh-golang dh-systemd golang
git clone https://github.com/sagostin/gofaxserver
cd gofaxserver
dpkg-buildpackage -us -uc -rfakeroot -b
```

## License

GNU General Public License v2.0
