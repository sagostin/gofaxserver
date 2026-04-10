# Architecture Overview

This document provides an overview of the fax relay system architecture.

## System Components

```
┌─────────────────────────────────────────────────────────────┐
│                     gofaxserver                            │
├───────────────┬─────────────┬──────────────┬───────────────┤
│ Event Socket  │   Router    │    Queue     │   Web API     │
│   Server      │             │              │               │
├───────────────┴─────────────┴──────────────┴───────────────┤
│                      PostgreSQL                             │
└─────────────────────────────────────────────────────────────┘
         │                                        │
         ▼                                        ▼
    FreeSWITCH                         HTTP Clients
    (mod_spandsp)                      (Admin/Tenants)
```

### Core Components

| Component | File | Description |
|-----------|------|-------------|
| **Event Socket Server** | `gofaxserver/freeswitch_inbound.go`, `freeswitch_outbound.go` | Handles FreeSWITCH communication for inbound/outbound fax |
| **Router** | `gofaxserver/router.go` | Routes incoming calls based on number-to-tenant mapping |
| **Queue** | `gofaxserver/queue.go` | Manages outbound fax jobs with retry logic and failover |
| **Web Server** | `gofaxserver/web.go` | RESTful API for administration and operations |
| **FaxTracker** | `gofaxserver/faxtracker.go` | Real-time tracking of in-flight fax jobs |

---

## Data Flow

### Inbound Fax (External → Customer PBX)

1. Call arrives at FreeSWITCH from upstream carrier
2. FreeSWITCH sends event to gofaxserver via Event Socket
3. `EventSocketServer` (`freeswitch_inbound.go`) parses the call event
4. Fax job is sent to `Router` via `FaxJobRouting` channel
5. `Router.routeFax()` (`router.go:44-130`) looks up tenant by called number
6. `Router.getEndpointsForNumber()` (`tenants.go:120`) finds endpoint(s)
7. Fax job queued to `Queue` for delivery to customer PBX gateway

### Outbound Fax (Customer → External)

1. Fax job created via `/fax/send` API endpoint (`web.go:390`)
2. `pdfToTiff()` (`tiff.go`) converts document to TIFF
3. Fax job sent to `Router` via `FaxJobRouting` channel
4. Router applies dialplan transformations (`DialplanManager`)
5. Endpoint lookup for source number's tenant
6. Fax queued to `Queue` for delivery to upstream gateways

---

## In-Memory Data Structures

```go
// gofaxserver/server.go:54-66
type Server struct {
    // Tenant storage
    Tenants         map[uint]*Tenant
    TenantNumbers   map[string]*TenantNumber
    
    // Endpoint storage
    TenantEndpoints map[uint][]*Endpoint    // keyed by tenant ID
    NumberEndpoints map[string][]*Endpoint  // keyed by phone number
    
    // Upstream gateways (global fallback)
    UpstreamFsGateways []string
    
    // Fax job channels
    FaxJobRouting chan *FaxJob
    Queue        *Queue
    Router       *Router
}
```

---

## T.38 Negotiation

### Pair State Tracking

```go
// gofaxserver/server.go:68-72
type T38PairState struct {
    LastUsedT38 bool      // what we actually used on last call
    LastSeen    time.Time // when that call finished
}

const T38PairTTL = 15 * time.Minute
```

### Negotiation Logic

```go
// gofaxserver/server.go:47-61
func (s *Server) ShouldAllowT38ForPair(srcNum, dstNum string, now time.Time) bool {
    // No recent history: start with T.38 allowed
    // Flip-flop: if we last used T.38, next call should NOT; and vice-versa
}
```

### Softmodem Fallback

Numbers with repeated T.38 failures are automatically flagged for G.711-only:

```go
// gofaxserver/softmodemfallback.go
func SetSoftmodemFallback(number string, enable bool) error
```

---

## Web API Routes

Defined in `gofaxserver/web.go:23-50`:

```
/admin/*  - Basic auth with API key
  GET  /reload           - Hot-reload config from DB
  GET  /faxes            - List active fax jobs
  POST /tenant           - Create tenant
  PUT  /tenant/{id}      - Update tenant
  DELETE /tenant/{id}     - Delete tenant
  POST /number           - Add tenant number
  PUT  /number/{id}      - Update number
  DELETE /number         - Delete number (query params)
  POST /endpoint         - Add endpoint
  PUT  /endpoint/{id}    - Update endpoint
  DELETE /endpoint/{id}  - Delete endpoint
  POST /user             - Create tenant user
  PUT  /user/{id}        - Update user
  DELETE /user/{id}      - Delete user
  POST /fallback         - Set softmodem fallback

/fax/*    - Tenant user auth
  POST /send             - Send fax
  GET  /status           - Check fax status by UUID
```

---

## Database Schema

### Tables

**tenants**
| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| name | string | Tenant name |
| notify | string | Notification settings |

**tenant_numbers**
| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| tenant_id | uint | Foreign key to tenants |
| number | string | Phone number (unique) |
| name | string | Caller ID name |
| header | string | Fax header |

**endpoints**
| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| type | string | "tenant", "number", or "global" |
| type_id | uint | TenantID or NumberID |
| endpoint_type | string | "gateway", "webhook", "email" |
| endpoint | string | Gateway name or URL |
| priority | uint | Lower = higher priority |
| bridge | bool | Enable T.38 transcoding |

**tenant_users**
| Column | Type | Notes |
|--------|------|-------|
| id | uint | Primary key |
| tenant_id | uint | Foreign key to tenants |
| username | string | Unique login |
| password | string | Encrypted |
| api_key | string | User's API key |

---

## Configuration

Configuration file: `/etc/gofaxserver/config.json`

```json
{
  "freeswitch": {
    "event_client_socket": "127.0.0.1:8021",
    "event_client_socket_password": "ClueCon",
    "event_server_socket": "127.0.0.1:8084",
    "softmodem_fallback": true
  },
  "faxing": {
    "enable_t38": true,
    "request_t38": true,
    "retry_delay": "300",
    "retry_attempts": "3"
  },
  "database": {
    "host": "localhost",
    "port": "5432",
    "user": "gofaxserver",
    "password": "...",
    "database": "gofaxserver"
  },
  "web": {
    "listen": ":8080",
    "api_key": "..."
  },
  "loki": {
    "push_url": "http://localhost:3100/loki/api/v1/push",
    "job": "faxserver"
  }
}
```

---

## Related Documentation

- [SETUP.md](SETUP.md) - Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) - Tenant and endpoint configuration
- [API_REFERENCE.md](API_REFERENCE.md) - Full API documentation
- [GATEWAYS.md](GATEWAYS.md) - FreeSWITCH gateway configuration
