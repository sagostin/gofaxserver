# Fax Portal (gofaxportal)

A standalone multi-tenant **outbound fax portal** for gofaxserver. It is a
separate Go module, separate binary, separate PostgreSQL database, and never
imports gofaxserver packages — it talks to gofaxserver exclusively over its
HTTP API. gofaxserver requires no configuration changes beyond what it already
exposes.

```
Browser ──(session cookie)──► Portal :8081 ──┬─(admin:<API_KEY>)──► gofaxserver /admin/*
   ▲                                         └─(svc_<org>:<pw>)──► gofaxserver /fax/send, /fax/status
 login page
```

Neither the admin API key nor service-account passwords ever reach the browser.

## Quick start (from zero)

0. **Deploy the updated gofaxserver** — it must be running a build that
   includes the read-only list endpoints (`GET /admin/tenants|numbers|users|
   endpoints`) or Admin → Reconcile will fail. Everything else works on older
   builds too.
1. **Create the portal database** (SQL under [Build & run](#build--run)) —
   schema auto-migrates on first start.
2. **Secrets & env** — `cd portal && cp sample.env .env`, fill the five
   required values (see [Configuration](#configuration) /
   [How config reaches the container](#how-config-reaches-the-container)):
   `PORTAL_SESSION_SECRET`, `PORTAL_ENCRYPTION_KEY` (back it up with the DB!),
   `PORTAL_ADMIN_API_KEY` (= gofaxserver `web.api_key`), `PORTAL_DB_PASSWORD`,
   `PORTAL_BOOTSTRAP_PASSWORD`.
3. **Start it** — `docker compose up -d --build` (host networking, :8081), or
   build the binary locally with `go build -o gofaxportal ./cmd/portal`.
4. **TLS** — `cp Caddyfile.sample Caddyfile`, set your hostname,
   `docker compose --profile tls up -d` (see [TLS with Caddy](#tls-with-caddy-recommended)).
5. **First login** — sign in with the bootstrap admin, then immediately
   Admin → Users → Reset PW (the bootstrap password lives in plaintext env
   until rotated); afterwards the bootstrap env vars can be blanked.
6. **Provision a customer** — Admin → Orgs: create org (auto-provisions the
   gofaxserver tenant + service account) → Numbers: add number → Users: create
   fax user → Numbers: assign number to the user (also wires the receipt
   email). If the customer needs a PBX gateway, follow
   [Adding a new customer whose PBX delivers/receives via FreeSWITCH](#adding-a-new-customer-whose-pbx-deliversreceives-via-freeswitch).
7. **Verify** — walk the [First-run smoke checklist](#first-run-smoke-checklist).

## Concepts

- **Organization** — a portal tenant. Creating one provisions a matching
  gofaxserver `tenant` plus a single generated **service account**
  (`svc_<slug>`) whose credentials are stored AES-256-GCM encrypted in the
  portal DB. Portal users are *not* created on gofaxserver.
- **Numbers** — each outbound number is registered on gofaxserver under the
  org's tenant and mirrored locally. Assigning a number to portal users both
  allows them to use it as caller ID and merges their emails into the
  number's `notify` string as `email_report->a@x;b@y`, so gofaxserver itself
  emails PDF completion receipts (see "How receipts actually work" below).
- **Fax users** — live only in the portal DB (bcrypt). They can send faxes
  using *assigned* numbers only and see their own job history with live status.
- **Admins** — global accounts that manage orgs, numbers, assignments, portal
  users, endpoints, active faxes, all jobs, and the audit log.
- **Poller** — reconciles job state every few seconds from `/fax/status`
  (per-org svc credentials) plus `/admin/faxes` (tracker snapshot) to derive
  terminal `success` / `failed` states.

## How receipts actually work (verified against gofaxserver source)

gofaxserver dispatches notifications at the end of every queued fax job
(`queue.go:processFax` → `notify.go:processNotifyDestinationsAsync`),
regardless of direction:

- **Outbound (portal sends):** destinations resolve from the **source side** —
  the *caller* number's `notify` first, falling back to the source tenant's.
  The router stamps `SrcTenantID` from the caller number
  (`router.go:routeFax:50-51`), so a number registered to an org receives its
  own transmission receipts. This is what the portal wires up on assignment.
- **Inbound receptions:** same mechanism from the destination side (callee
  number first, then tenant). A number therefore also gets reception notices —
  harmless for outbound-only assignments.

Behavior details worth knowing:

- `email_report->…` fires **once per job when all attempts conclude — success
  or failure** — attaching a PDF report (caller/callee/timestamps + per-attempt
  status table). It does not attach the fax itself (`email_full` does), and it
  is not failure-gated (`email_full_failure` is).
- Multiple recipients on one entry are `;`-separated (the SMTP layer splits).
- Number-level `notify` overrides tenant-level when non-empty; the portal only
  ever writes number-level strings derived from assigned users.
- Lookups are exact string matches on the registered number, so numbers must
  be stored in the dialplan-normalized format (e.g. 10 digits) — the same
  value used as `caller_number` in `/fax/send`.
- Success-only / failure-only *report* emails are not supported natively and
  would require a gofaxserver change (deliberately out of scope).

## Adding a new customer whose PBX delivers/receives via FreeSWITCH

The portal provisions gofaxserver-side state (tenant, service account,
numbers, endpoints). FreeSWITCH gateway configuration can also be automated
when gofaxserver has `freeswitch.gateway_config_dir` set (a path shared with
the FreeSWITCH host/container, e.g. `/etc/freeswitch/gateways`):

**Admin → Gateways** — pick a template (`sbc` for upstream carriers, `pbx`
for customer PBXs), enter the gateway name (e.g. `pbx_<customer>`), the
`realm` (PBX/SBC host or IP), optional SIP-auth credentials
(`register` + username/password), the scope (tenant/number/global), priority,
and whether to use bridge/transcoding mode. Submitting renders the XML into
the gateways directory, reloads the `fax` sofia profile over the event
socket, and creates the matching endpoint in one step. The table shows each
gateway's live `sofia status` state — registered gateways additionally show
the monitor's tracked `last_state`.

The Gateways tab also surfaces **drift** between the database and disk:
XML files present in the directory with no DB record appear under
"Unmanaged files" with **Adopt** / **Delete** actions, and managed gateways
whose file went missing show a **Re-render** repair action. Endpoints created
by provisioning are marked "⚙ managed" on the Endpoints tab (edit/delete
there is disabled; gofaxserver's API refuses such edits with 409).

Templates themselves are database-backed and editable under **Admin →
Templates** (Go template syntax; declared `{{.variables}}` drive the
provision form). The dialplan is editable under **Admin → Dialplan** when
gofaxserver runs with `dialplan.source = "db"` (the tab shows a banner when
config-file mode is active and the rules are inactive). Full parameter
reference: [GATEWAYS.md](GATEWAYS.md).

When provisioning is **not** enabled (no `gateway_config_dir`, or no shared
filesystem with FreeSWITCH), the gateway XML must still be created manually
on the FreeSWITCH host and loaded with `fs_cli -x "sofia profile fax rescan"`
— see [GATEWAYS.md](GATEWAYS.md); then register the matching endpoint in the
portal (**Admin → Endpoints** or `POST /portal/api/admin/endpoints`):
`type=tenant`, `type_id=<org>`, `endpoint_type=gateway`,
`endpoint=pbx_<customer>:<PBX_IP>`, desired priority (use `666` for
outbound-only, i.e. no inbound delivery).

## Layout

| Path | Purpose |
|------|---------|
| `cmd/portal/` | entrypoint |
| `internal/config` | config + env overrides |
| `internal/models`, `internal/db` | GORM models + migration (own DB) |
| `internal/auth` | bcrypt, sessions, rate limiting |
| `internal/crypto` | AES-GCM secret sealing |
| `internal/fsclient` | typed gofaxserver API client (incl. reconciliation lists) |
| `internal/api` | Iris HTTP API + RBAC middleware + audit |
| `internal/poller` | job status reconciliation loop |
| `frontend/` | Vue 3 + Vite SPA (embedded into the binary via `go:embed`) |

## Configuration

Copy `config.json.sample` to `config.json` or configure purely via env vars:

| Env | Purpose |
|-----|---------|
| `PORTAL_LISTEN` | listen addr (default `:8081`) |
| `PORTAL_DB_*` | `HOST`, `PORT`, `USER`, `PASSWORD`, `NAME`, `SSLMODE` |
| `PORTAL_SESSION_SECRET` | required; any long random string |
| `PORTAL_ENCRYPTION_KEY` | required; long random string (seals svc passwords) |
| `PORTAL_GOFAX_BASE_URL` | default `http://127.0.0.1:8080` |
| `PORTAL_ADMIN_API_KEY` | required; matches gofaxserver `web.api_key` |
| `PORTAL_BOOTSTRAP_USERNAME/PASSWORD/EMAIL` | creates the first admin when DB empty |

On first start with an empty user table and a bootstrap password set, an
initial admin account is created.

> **Why a bootstrap password even though gofaxserver is already deployed?**
> Portal logins never touch gofaxserver's `tenant_users` — users live only in
> the portal's own DB (bcrypt), which starts **empty**. Without one initial
> account there is no way to sign in and create admins/orgs. The bootstrap
> vars are used exactly once (when the user table is empty); afterwards you
> can blank them and manage all further users in the Admin → Users UI. If
> omitted on an empty DB, the portal starts but logs a warning and nobody can
> log in until the var is set and the portal restarts.

### How config reaches the container

Both modes work — pick one:

- **Env-only (default in `portal/docker-compose.yml`):** no file needed. The
  container's CMD passes `-c /etc/gofaxportal/config.json`, but a missing file
  is explicitly allowed and env vars fully configure the portal. Fill
  `portal/.env` (from `portal/sample.env`) or export the `PORTAL_*` vars.
- **Config file:** uncomment the volume line in `portal/docker-compose.yml`
  to mount it read-only:
  ```yaml
  volumes:
    - ./config.json:/etc/gofaxportal/config.json:ro
  ```
  Env vars still override file values when both are present.

Required either way: session secret, encryption key, admin API key, DB
credentials, and a bootstrap password for first run:

```bash
openssl rand -hex 32   # PORTAL_SESSION_SECRET
openssl rand -hex 32   # PORTAL_ENCRYPTION_KEY (back this up with the DB!)
```

## TLS with Caddy (recommended)

Use `portal/Caddyfile.sample`: copy it to `portal/Caddyfile`, set your DNS
name, then start the optional Caddy profile (host networking, so it binds
80/443 directly; certificates are issued/renewed automatically):

```bash
cd portal && cp Caddyfile.sample Caddyfile && $EDITOR Caddyfile
docker compose --profile tls up -d
```

The sample Caddyfile fronts **both** services on one hostname:

- `/portal/*` → portal (`127.0.0.1:8081`)
- everything else (`/fax/*`, `/admin/*`, `/tenant/*`, `/health`) → gofaxserver
  (`127.0.0.1:8080`), proxied through byte-for-byte — existing API clients
  (Basic-auth curl scripts etc.) keep working on the hostname unchanged, and
  can also keep hitting `:8080` directly if it's firewalled to them.

The sample also includes a commented hardening block that IP-restricts
`/admin/*` while leaving tenant-user fax endpoints public. If you do **not**
want gofaxserver's admin API on the hostname at all, delete the fallback
`handle` block instead and expose only `/portal/*` — clients then continue
using direct `:8080` access.

Once HTTPS is live keep `PORTAL_COOKIE_SECURE=true` (the compose default) so
session cookies are marked Secure.

## Build & run

```bash
cd portal/frontend && npm install && npm run build   # outputs to internal/web/dist
cd .. && go build -o gofaxportal ./cmd/portal
./gofaxportal -c config.json
```

Docker (host networking to match the root compose):

```bash
docker compose -f portal/docker-compose.yml up -d --build
```

Database: create a `gofaxportal` database and user (schema auto-migrates):

```sql
CREATE DATABASE gofaxportal;
CREATE USER gofaxportal WITH PASSWORD '...';
GRANT ALL PRIVILEGES ON DATABASE gofaxportal TO gofaxportal;
GRANT ALL ON SCHEMA public TO gofaxportal;
```

## First-run smoke checklist

After deployment, walk this once against your live gofaxserver:

1. `curl https://<host>/portal/api/health` → `{"status":"ok"}`
2. Log in with the bootstrap admin → change its password (Admin → Users → Reset PW)
3. Create an org (Admin → Orgs) — this provisions the gofaxserver tenant + `svc_*` account
4. Add a number to the org, create a fax user, assign the number to them
5. Log in as the user, send a small test PDF
6. Watch My Faxes flip `queued → sending → success/failed` (poller ticks every ~5 s)
7. Confirm the `email_report` receipt lands in the user's inbox
8. Admin → Orgs → **Reconcile** should report a clean diff

## API surface (session cookie + CSRF)

## Path layout (behind Caddy or direct)

The portal serves everything under the `/portal` prefix so one hostname can
front both services without breaking existing gofaxserver API clients:

```
/portal/*        → portal UI (SPA, Vue)
/portal/api/*    → portal API (session cookie + CSRF)
/fax/*, /admin/*, /tenant/*, /health → gofaxserver (unchanged)
```

Direct access still works too: `http://<host>:8081/portal` (portal) and
`http://<host>:8080/fax/...` (gofaxserver). Session cookies are scoped with
`Path=/portal`.

Health: `GET /portal/api/health` (unauthenticated, for monitoring/compose/Caddy
healthchecks).

Auth: `POST /portal/api/auth/login`, `POST /portal/api/auth/logout`, `GET /portal/api/auth/me`.

Fax users: `GET /portal/api/me/numbers`, `POST /portal/api/faxes` (multipart:
`file`,`caller_number`,`callee_number`), `GET /portal/api/faxes[?status=]`,
`GET /portal/api/faxes/{id}`.

Admin (`role=admin`): CRUD under `/portal/api/admin/{orgs,numbers,users,endpoints}`,
gateway provisioning under `/portal/api/admin/{gateways,gateway-templates}`
(including `gateways/adopt`, `gateways/unmanaged/{name}`, `gateways/{name}/repair`),
dialplan rules under `/portal/api/admin/dialplan`,
`PUT /portal/api/admin/numbers/{id}/assignments`, `GET /portal/api/admin/orgs/{id}/reconcile`,
`GET /portal/api/admin/faxes/active`, `GET /portal/api/admin/jobs[?org_id=&status=]`,
`GET /portal/api/admin/jobs/{id}/live`, `GET /portal/api/admin/audit`.

Mutating requests require the `X-CSRF-Token` header returned by login/me.

## Security notes

- bcrypt(12) password hashes; 256-bit session tokens stored only as SHA-256.
- HttpOnly + SameSite=Lax cookies (enable `cookie_secure` behind TLS).
- Per-user number allowlist enforced server-side before proxying upstream;
  gofaxserver independently enforces tenant-level ownership of caller numbers.
- Upload validation mirrors gofaxserver (extension + magic-byte sniffing + size cap).
- Login/send rate limiting; full admin audit trail.
- Sessions slide: renewed past the halfway point of their 24 h lifetime; all
  of a user's sessions are revoked on password reset or deactivation.
- Jobs that never reach a terminal state (e.g. gofaxserver restarted before
  writing attempt rows) are force-marked `failed` after 24 h by the poller.
- Gateway provisioning runs on gofaxserver behind its admin API key; the
  portal only proxies it for `role=admin` users and audit-logs every action.
  Gateway secrets (SIP passwords) are stored encrypted by gofaxserver
  (`psk`), masked as `********` in API responses, and template params are
  XML-escaped at render time so values cannot inject markup into FreeSWITCH
  config.

## Reconciliation

Because gofaxserver previously had no list APIs, drift was possible if people
edited it out-of-band. It now exposes read-only `GET /admin/tenants|numbers|
users|endpoints`; the portal's per-org **Reconcile** button diffs the mirror
against live state (missing tenants/numbers/users, ID mismatches). The rule:
**manage gofaxserver tenants through the portal**, treat CLI/curl edits as
exceptional, and re-run reconcile after them.
