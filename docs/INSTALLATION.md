# Installation Guide

End-to-end installation of the gofaxserver stack: **FreeSWITCH** (SIP/T.38
termination) + **PostgreSQL** + **gofaxserver** (routing/API) + optional
**fax portal** (end-user web UI) with TLS.

By the end of this guide you will have a verified, running system. For
day-2 work — adding customers, gateways, numbers — see
[SETUP.md](SETUP.md) (tenant onboarding) and [PORTAL.md](PORTAL.md).

## Components

```
                 ┌────────────────────────── gofaxserver host ──────────────────────────┐
 SIP peers  ◄──► │ FreeSWITCH (mod_sofia "fax" profile, mod_spandsp)                    │
 (PBX/SBC,  SIP  │   ▲ ESL :8021 (localhost)      dialplan → socket :8022 (localhost)   │
 carriers)  RTP  │   ▼                              ▲                                   │
                 │ gofaxserver (API :8080) ──► PostgreSQL :5432                         │
                 │   ▲                                                                  │
                 │   └── HTTP ── fax portal :8081 (optional) ◄── browsers               │
                 └───────────────────────────────▲──────────────────────────────────────┘
                                                 └── Caddy :80/:443 (optional TLS)
```

- **FreeSWITCH** terminates/originates SIP and T.38/G.711 media. Its config
  is minimal and lives in `examples/freeswitch/`.
- **gofaxserver** talks to FreeSWITCH over the Event Socket only (outbound
  commands to `:8021`, inbound call events on `:8022`) — no shared modules.
- **PostgreSQL** stores all gofaxserver state (auto-migrated) and, if
  deployed, the portal's separate database.
- **Portal** (`portal/`) is optional but recommended; it automates tenant,
  number, user, and gateway management.

## Choose your deployment path

| | **Path A — all containers** (recommended) | **Path B — FreeSWITCH on the host** |
|---|---|---|
| FreeSWITCH | Docker container (built from `examples/freeswitch/Dockerfile`) | Debian host service (SignalWire apt) |
| gofaxserver + PostgreSQL | Docker (`docker-compose.full.yml`) | Docker (`docker-compose.yml`) or bare-metal `.deb` |
| Best for | New deployments, single node, simplest networking | Existing FreeSWITCH hosts, custom FS modules, SIP capture tooling on host |
| SignalWire token needed | At image **build** time | At **apt** setup time |

Both paths use **host networking** for all containers: FreeSWITCH's ESL binds
`127.0.0.1:8021` and the fax profile's dialplan points at
`socket:127.0.0.1:8022`, so with host networking gofaxserver and FreeSWITCH
reach each other on localhost with zero configuration changes, and SIP/RTP
bind directly on the host (no NAT/port-range mapping pain for RTP media).

The portal is deployed the same way on top of either path
([step 4](#4-portal--tls-optional)).

## Requirements

### Software

- **OS**: Debian 12 (bookworm) recommended (any Docker-capable Linux works
  for Path A)
- **Docker Engine 24+** with the Compose plugin
- **SignalWire personal access token** — required to install FreeSWITCH
  packages in *both* paths ([how to create one](https://developer.signalwire.com/freeswitch/FreeSWITCH-Explained/Installation/how-to-create-a-personal-access-token))
- Path B bare-metal gofaxserver only: Go 1.22+ (build), ImageMagick 7+ and
  Ghostscript (PDF→TIFF conversion)

### Hardware (per node)

| Scale | vCPU | RAM | Disk |
|---|---|---|---|
| Small (< 20 concurrent calls) | 2 | 2 GB | 20 GB + DB growth |
| Medium | 4 | 4 GB | 50 GB + DB growth |

Fax is low-throughput; the database and FreeSWITCH logs dominate disk use.

### Network / ports

| Port | Proto | Purpose | Expose to |
|---|---|---|---|
| 5060 | tcp+udp | Sofia `fax` profile — SIP signaling | your PBXs / carriers |
| 16384–32768 | udp | RTP media (FreeSWITCH default range) | your PBXs / carriers |
| 8021 | tcp | FreeSWITCH Event Socket | **localhost only** (bound to 127.0.0.1) |
| 8022 | tcp | gofaxserver inbound ESL listener | **localhost only** |
| 8080 | tcp | gofaxserver REST API | localhost / trusted clients (or via Caddy) |
| 8081 | tcp | Fax portal | localhost (front with Caddy) |
| 5432 | tcp | PostgreSQL | **localhost only** |
| 80/443 | tcp | Caddy TLS termination (optional) | public |

Firewall 5060 and the RTP range to known peer IPs where possible; everything
else should stay on localhost or behind Caddy.

---

## Path A — all containers (recommended)

Everything runs from one compose file: `docker-compose.full.yml`.

### 1. Get the source and prepare secrets

```bash
git clone <repo-url> gofaxserver && cd gofaxserver

cp sample.env .env
cp config.json.sample config.json
```

Edit `.env`:

```bash
# Generate strong values:
openssl rand -hex 32   # use for POSTGRES_PASSWORD
```

- `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` — database
  credentials. **They must match `database.*` in `config.json`** (the samples
  intentionally differ — align them).
- `SIGNALWIRE_TOKEN` — your SignalWire token (used only at image build time;
  it is passed as a Docker build secret and never stored in the image).

Edit `config.json` (see [Configuration](#configuration)):

- `database.user` / `database.password` / `database.database` — match `.env`
- `web.api_key` — `openssl rand -hex 32` (admin API key; the portal needs it)
- `psk` — `openssl rand -hex 32` (encrypts tenant-user and gateway
  credentials; **back it up — losing it makes stored secrets unreadable**)
- `freeswitch.gateway_config_dir` — keep `/etc/freeswitch/gateways` (the
  compose file shares exactly this path between the containers)
- `smtp.*` — required for email receipts/notifications
- `loki.*` — optional; remove/ignore the section if you don't run Loki

Lock the files down:

```bash
chmod 600 .env config.json
```

### 2. Set the FreeSWITCH IP (the one mandatory FreeSWITCH edit)

Edit `examples/freeswitch/vars.xml`:

```xml
<X-PRE-PROCESS cmd="set" data="sofia_ip=192.0.2.10"/>  <!-- THIS host's LAN IP -->
```

`sofia_ip` is used for the SIP **and** RTP bind addresses *and* the
advertised (external) IP. A wrong value breaks signaling or produces one-way
audio. Everything else in `examples/freeswitch/` works as shipped.

### 3. Prepare the shared directories

**Gateway directory** — API-driven gateway provisioning (portal **Admin →
Gateways**) renders gateway XML into a directory shared by both containers at
the **same path**:

```bash
mkdir -p volumes/gateways
sudo chown 1000:1000 volumes/gateways   # gofaxserver's container user is uid 1000
```

(FreeSWITCH runs as root in its container and reads the resulting `0640`
files without further setup.)

**Fax temp directory** — no action needed: `docker-compose.full.yml` shares a
named volume (`faxtmp`) between both containers at `/var/lib/gofaxserver/tmp`
(matching `faxing.temp_dir` in the sample config), and the image pre-creates
it with the right ownership. See [Fax temp storage &
cleanup](#fax-temp-storage--cleanup) for why this share exists.

### 4. Build and start

```bash
export SIGNALWIRE_TOKEN=your-token        # consumed as a build secret
docker compose -f docker-compose.full.yml up -d --build
```

Then [verify](#post-install-verification).

---

## Path B — FreeSWITCH as a Debian host service

### 1. Install FreeSWITCH (Debian 12)

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

### 2. Install the gofaxserver FreeSWITCH configuration

```bash
cp -r examples/freeswitch/. /etc/freeswitch/
$EDITOR /etc/freeswitch/vars.xml     # REQUIRED: sofia_ip = this host's LAN IP
mkdir -p /etc/freeswitch/gateways
```

The `fax` Sofia profile (`autoload_configs/sofia.conf.xml`) already includes
`/etc/freeswitch/gateways/*.xml` and points its dialplan at
`socket:127.0.0.1:8022 async full`.

A systemd unit is provided (stock packages run FreeSWITCH as root — the unit
documents the optional hardening to a dedicated user):

```bash
cp examples/freeswitch.service /etc/systemd/system/freeswitch.service   # optional override
systemctl daemon-reload && systemctl enable --now freeswitch
```

### 3a. gofaxserver + PostgreSQL in Docker (usual choice)

Use the root `docker-compose.yml` (FreeSWITCH is not in it — it's the host
service you just installed):

```bash
cp sample.env .env && $EDITOR .env                     # DB credentials
install -m 600 -o root config.json /etc/gofaxserver/config.json  # then edit it

# Shared fax temp dir: the host FreeSWITCH and the gofaxserver container must
# see store-and-forward files at the SAME path (faxing.temp_dir):
sudo mkdir -p /var/lib/gofaxserver/tmp
sudo chown 1000:1000 /var/lib/gofaxserver/tmp   # container user is uid 1000

docker compose up -d --build
```

The shipped compose mounts `/var/lib/gofaxserver/tmp` at the same path in the
container — keep `faxing.temp_dir` at the sample value. (If you are migrating
an older install whose config uses `temp_dir=/faxtmp`, keep the legacy
`/faxtmp` mount line in `docker-compose.yml` instead.)

To enable API-driven provisioning, uncomment the
`/etc/freeswitch/gateways` volume line in `docker-compose.yml`, keep
`freeswitch.gateway_config_dir: /etc/freeswitch/gateways` in `config.json`,
and make the directory writable by the container user (uid 1000):

```bash
sudo mkdir -p /etc/freeswitch/gateways
sudo chgrp 1000 /etc/freeswitch/gateways && sudo chmod g+ws /etc/freeswitch/gateways
```

FreeSWITCH (running as root) reads the `0640` files gofaxserver writes. If
you hardened FreeSWITCH to a dedicated user, follow the group/setgid recipe
in [GATEWAYS.md](GATEWAYS.md) and consider `freeswitch.gateway_config_chown`.

### 3b. Bare-metal gofaxserver (alternative)

Build the `.deb` from `debian/` (`dpkg-buildpackage`) or the binary from
source (`go build -o gofaxserver ./gofaxserver/cmd/gofaxserver`), then follow
[debian/README.Debian](../debian/README.Debian). Notes:

- The bundled systemd unit runs as `gofax` with `ProtectSystem=strict`;
  `ReadWritePaths` already covers `/var/lib/gofaxserver` (which contains the
  default `faxing.temp_dir`), `/tmp`, and `/etc/freeswitch/gateways`
  (provisioning). If you point `temp_dir` anywhere else, extend
  `ReadWritePaths`. The host FreeSWITCH (root) reads/writes the temp dir at
  the same path — if you hardened FreeSWITCH to a dedicated user, give that
  user access to `/var/lib/gofaxserver/tmp` as well.
- Install ImageMagick 7+ and Ghostscript — outbound PDF→TIFF conversion
  shells out to them.
- PostgreSQL runs on the host in this variant; create the database per the
  SQL in [Configuration](#configuration) / README.

---

## 4. Portal + TLS (optional, recommended)

The portal gives end users a web UI and automates tenant/gateway management.
Full details: [PORTAL.md](PORTAL.md). Short version (works on both paths):

```bash
# Portal database (same PostgreSQL instance):
psql -U postgres -c "CREATE DATABASE gofaxportal;" \
     -c "CREATE USER gofaxportal WITH PASSWORD '...';" \
     -c "GRANT ALL PRIVILEGES ON DATABASE gofaxportal TO gofaxportal;" \
     -c "GRANT ALL ON SCHEMA public TO gofaxportal;" gofaxportal

cd portal
cp sample.env .env && $EDITOR .env
#   PORTAL_SESSION_SECRET / PORTAL_ENCRYPTION_KEY: openssl rand -hex 32
#   PORTAL_ADMIN_API_KEY = gofaxserver web.api_key
#   PORTAL_DB_*          = the database above
#   PORTAL_BOOTSTRAP_PASSWORD = first admin login (rotate after first use)
docker compose up -d --build                            # portal on :8081

# TLS (recommended): one hostname fronts portal + gofaxserver API
cp Caddyfile.sample Caddyfile && $EDITOR Caddyfile      # set your DNS name
docker compose --profile tls up -d                      # Caddy on :80/:443
```

First login: sign in with the bootstrap admin and immediately reset its
password (**Admin → Users → Reset PW**). Keep `PORTAL_COOKIE_SECURE=true`
once HTTPS is live.

---

## Configuration

gofaxserver reads `/etc/gofaxserver/config.json` (Path B) or the mounted
`./config.json` (Path A). Annotated example: `config.json.sample`; the full
key-by-key reference with defaults is in the
[README](../README.md#configuration). Highlights:

| Section | Notes |
|---|---|
| `freeswitch.*` | ESL endpoints/password (`event_client_socket_password` must match `autoload_configs/event_socket.conf.xml`), `gateway_config_dir` (provisioning), `gateway_profile` (default `fax`), `gateway_monitor_seconds` (default 60) |
| `faxing.*` | `temp_dir` (must be shared with FreeSWITCH at the same path — see below), `temp_max_age` (janitor, default `24h`), retry policy, T.38 flags, `policy` (fax policy engine thresholds — optional, defaults apply) |
| `database.*` | must match the PostgreSQL credentials from `.env` |
| `web.*` | listen addr + admin `api_key` |
| `dialplan` | `source: "config"` (rules in this file) or `"db"` (portal/API-editable); absent = built-in NANP defaults |
| `smtp.*` | required for email notifications/receipts |
| `loki.*` | optional log shipping |
| `psk` | encryption key for stored secrets — **back it up with the database** |

PostgreSQL (Path B / bare-metal) needs the database and user once — schema is
auto-migrated on startup:

```sql
CREATE DATABASE gofaxserver;
CREATE USER gofaxserver WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE gofaxserver TO gofaxserver;
GRANT ALL ON SCHEMA public TO gofaxserver;   -- required for auto-migration
```

---

## Fax temp storage & cleanup

gofaxserver is **store-and-forward**: for relayed (non-bridge) calls the fax
is written to `faxing.temp_dir` as a file by one side and read by the other.
That means **FreeSWITCH and gofaxserver must see the same directory at the
same path**:

- Outbound: gofaxserver converts the uploaded PDF to `fax_<id>.tiff` in
  `temp_dir`, then tells FreeSWITCH (over ESL) to transmit *that path*.
- Inbound (non-bridge): gofaxserver tells FreeSWITCH to receive into
  `temp_dir/fax_<uuid>.tiff`, then processes the file when the call ends.

Both compose files wire this up (`/var/lib/gofaxserver/tmp`, matching the
sample config); on bare-metal the host directory works as-is. If you change
`temp_dir`, keep the mounts in sync.

These files contain **sensitive fax content**, so their lifetime is bounded:

- **Completed jobs** delete their files immediately (all directions,
  including notification/report PDFs).
- **Orphans** (crashes, restarts, failed conversions) are removed by a
  built-in **janitor** that sweeps `temp_dir` every 10 minutes and deletes
  gofaxserver-created files (`fax_*`, `temp_*`, `notify_*`, `first_*`) older
  than `faxing.temp_max_age` — default `24h`, accepts `s/m/h/d` suffixes,
  `"0s"` disables. Lower it if your compliance posture requires shorter
  retention (keep it above your longest plausible job, e.g. retries).
- The directory is created with `0750` permissions where possible.

**Encryption at rest is deliberately not applied** to these temporary files:
FreeSWITCH — a separate process, often in a different container — must read
and write them, so any scheme would reduce to obfuscation with real
operational cost. Protection comes from restrictive permissions, prompt
deletion on completion, and the janitor bounding worst-case exposure. If you
need stronger guarantees, place `temp_dir` on an encrypted filesystem (LUKS
or an encrypted volume) — transparent to both processes.

Do **not** include `temp_dir` in backups (transient, sensitive) — see
[BACKUP.md](../BACKUP.md).

---

## Post-install verification

Run these once after either path (substitute `docker exec freeswitch …` for
`fs_cli` on Path A if you have no host `fs_cli`):

1. **Containers/services up**
   ```bash
   docker compose -f docker-compose.full.yml ps     # or: docker compose ps / systemctl status freeswitch
   ```
2. **FreeSWITCH healthy, fax profile up**
   ```bash
   fs_cli -x "status" && fs_cli -x "sofia status"
   ```
3. **ESL link gofaxserver ↔ FreeSWITCH** — gofaxserver logs show a successful
   Event Socket connection on startup (`docker logs gofaxserver`); no
   `connection refused` retries.
4. **API up**
   ```bash
   curl -u "admin:$(grep -oP '\"api_key\":\s*\"\K[^\"]+' config.json)" \
        http://localhost:8080/admin/faxes
   ```
5. **Provisioning smoke test** (if `gateway_config_dir` set): create a gateway
   via portal **Admin → Gateways** or `POST /admin/gateway`, then:
   ```bash
   fs_cli -x "sofia status gateway <name>"     # shows the gateway (non-register gateways: NOREG)
   ```
6. **Portal** (if deployed): `curl http://localhost:8081/portal/api/health`
   → `{"status":"ok"}`, log in, create an org, walk the
   [first-run smoke checklist](PORTAL.md#first-run-smoke-checklist).
7. **End-to-end fax** — add a tenant/number/endpoint per
   [SETUP.md](SETUP.md) (or via the portal), send a small PDF, and watch the
   job reach a terminal state; check inbound delivery the same way.

---

## Security hardening checklist

- [ ] Strong unique `web.api_key`, `psk`, DB passwords, portal secrets
      (`openssl rand -hex 32`); `chmod 600` on `config.json` and `.env`
- [ ] `psk` and portal `PORTAL_ENCRYPTION_KEY` backed up with the databases
      (see [BACKUP.md](../BACKUP.md))
- [ ] Firewall: 5060 + RTP range restricted to known peers; 8021/8022/5432
      localhost-only; 8080/8081 not exposed publicly (front with Caddy)
- [ ] Caddy: enable the commented IP-restriction block for `/admin/*` in
      `portal/Caddyfile.sample` if admin API exposure worries you
- [ ] `faxing.temp_dir` on an encrypted filesystem if fax content at rest is
      in scope for your threat model; `temp_max_age` tuned to your retention
      policy (default 24h janitor sweep)
- [ ] `PORTAL_COOKIE_SECURE=true` behind HTTPS; bootstrap password rotated
- [ ] Loki/remote logs treated as sensitive (they contain fax metadata)

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| Gateway shows `not-loaded` in `sofia status` | The profile doesn't include the gateways dir — check `<X-PRE-PROCESS cmd="include" data="../gateways/*.xml"/>` in `sofia.conf.xml`, then `sofia profile fax rescan` |
| Inbound calls rejected (403) | `fsGatewayACL` matches the endpoint value `name:IP` **exactly** on the IP part — fix the endpoint, or the peer's source IP changed |
| gofaxserver logs ESL `connection refused` | FreeSWITCH not up, or `event_client_socket`/`event_client_socket_password` mismatch with `event_socket.conf.xml` (port 8021, password `ClueCon` by default) |
| Outbound fax fails converting PDF | `faxing.temp_dir` not writable by gofaxserver (check mount ownership — uid 1000 in containers), or (bare-metal) ImageMagick/Ghostscript missing |
| Fax fails with FreeSWITCH file errors (`txfax`/`rxfax` can't open path) | `temp_dir` not shared at the **same path** between gofaxserver and FreeSWITCH — check the compose mounts / host directory |
| Temp directory filling up | Janitor disabled or `temp_max_age` too high — check startup logs for "temp file janitor running"; completed jobs should never leave files |
| One-way audio / no media | `sofia_ip` in `vars.xml` wrong, or RTP range 16384–32768/udp blocked |
| Provisioning logs chown warnings | gofaxserver lacks permission for `gateway_config_chown` — run with `CAP_CHOWN` or clear the setting; files are still written |
| Portal login silently fails over http:// | `PORTAL_COOKIE_SECURE=true` without TLS — set `false` for plain-HTTP testing |
| FreeSWITCH image build fails at apt | Missing/expired `SIGNALWIRE_TOKEN` (build secret) |

## Where to go next (usage)

- **[SETUP.md](SETUP.md)** — tenant onboarding: customers, gateways,
  endpoints, numbers, priorities, test-fax procedure
- **[PORTAL.md](PORTAL.md)** — portal concepts, receipts, reconciliation,
  gateway/template/dialplan management
- **[GATEWAYS.md](GATEWAYS.md)** — FreeSWITCH gateway parameters and
  API-driven provisioning details
- **[API_REFERENCE.md](API_REFERENCE.md)** — REST API
- **[BACKUP.md](../BACKUP.md)** — backup/restore (databases, FreeSWITCH
  config, secrets)
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — internals

## Related Documentation

- [SETUP.md](SETUP.md) — Tenant onboarding runbook
- [PORTAL.md](PORTAL.md) — Fax portal deployment & operation
- [GATEWAYS.md](GATEWAYS.md) — Gateway configuration & provisioning
- [TENANTS.md](TENANTS.md) — Tenant modes and endpoint configuration
- [API_REFERENCE.md](API_REFERENCE.md) — gofaxserver REST API
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
