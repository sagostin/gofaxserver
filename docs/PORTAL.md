# Fax Portal (gofaxportal)

A standalone multi-tenant **send & receive fax portal** for gofaxserver. It is a
separate Go module, separate binary, separate PostgreSQL database, and never
imports gofaxserver packages — it talks to gofaxserver exclusively over its
HTTP API (outbound + admin) and receives inbound faxes from gofaxserver over a
single delivery endpoint (`POST /portal/api/inbound/<svc_username>`, see
[Receiving faxes](#receiving-faxes-inbound)).

```
Browser ──(session cookie)──► Portal :8081 ──┬─(admin:<API_KEY>)──► gofaxserver /admin/*
   ▲                                         └─(svc_<org>:<pw>)──► gofaxserver /fax/send, /fax/status
 login page
gofaxserver ──(POST /portal/api/inbound/<svc>, X-API-Key)──► Portal :8081
```

Neither the admin API key nor service-account passwords ever reach the browser.

## Quick start (from zero)

0. **Deploy the updated gofaxserver** — for full functionality it must be
   running a current build: the read-only list endpoints (`GET
   /admin/tenants|numbers|users|endpoints`) or Admin → Reconcile fails; the
   `portal` endpoint type or inbox delivery fails; the `portal` notify type
   or outbound status pushes are silently skipped (the poller still covers
   status, just delayed). Older builds degrade to outbound-only with manual
   gateway work.
1. **Create the portal database** (SQL under [Build & run](#build--run)) —
   schema auto-migrates on first start.
2. **Secrets & env** — `cd portal && cp sample.env .env`, fill the five
   required values (see [Configuration](#configuration) /
   [How config reaches the container](#how-config-reaches-the-container)):
   `PORTAL_SESSION_SECRET`, `PORTAL_ENCRYPTION_KEY` (back it up with the DB!),
   `PORTAL_ADMIN_API_KEY` (= gofaxserver `web.api_key`), `PORTAL_DB_PASSWORD`,
   `PORTAL_BOOTSTRAP_PASSWORD`.
3. **Start it** — `docker compose up -d --build` (host networking, :8081), or
   build the binary locally: `make portal-build` from the repo root (builds
   the frontend dist, then the binary to `bin/gofaxportal`), or by hand
   `cd portal/frontend && npm install && npm run build` then
   `cd portal && go build -o gofaxportal ./cmd/portal`.
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
- **Inbound (receiving)** — numbers with the **Inbox** toggle on (default)
  get a `portal`-type endpoint auto-provisioned on gofaxserver; faxes
  received on them are delivered into the portal, stored **encrypted at
  rest**, and shown to the users assigned to that number. See
  "Receiving faxes" below.

## Receiving faxes (inbound)

The portal is no longer outbound-only: each org's numbers can receive faxes
straight into the portal inbox.

**How delivery works.** When an admin adds a number with Inbox enabled, the
portal creates a gofaxserver endpoint `type=number`,
`endpoint_type=portal`, `endpoint=<org's svc_username>` (toggling Inbox off,
or deleting the number, removes it again). When a fax arrives for that
number, gofaxserver converts it to PDF and POSTs it to
`<portal.url>/portal/api/inbound/<svc_username>` — the same JSON payload and
retry/backoff semantics as `webhook` endpoints (`FaxJobWithFile`: flattened
job fields + base64 PDF, `X-File-SHA256`/`X-File-Bytes` headers). The portal
URL and an optional pre-shared key live in gofaxserver's config:

```json
"portal": { "url": "http://127.0.0.1:8081", "api_key": "shared-secret" }
```

`portal.api_key` must match the portal's `inbound_api_key` /
`PORTAL_INBOUND_API_KEY`; the portal rejects deliveries with a wrong or
missing key (401). Both empty is acceptable when the services are
loopback-only. Unknown service accounts (e.g. backend tenants not managed by
the portal) get a non-retriable 404.

**Encryption at rest.** Received PDFs are sealed with AES-256-GCM before
they touch the database (`inbound_faxes.file_enc` bytea). The key is
domain-separated from the service-account password box — both derive from
`encryption_key`, but blobs sealed for one purpose cannot be opened with the
other box. The plaintext SHA-256 + size are stored alongside and re-verified
on every decryption. Losing/rotating `encryption_key` makes stored faxes
undecryptable, so back it up with the DB. Plaintext exists only in memory
while serving a download.

**Visibility.** A fax is linked to the org (via the service account) and to
the mirrored number (via the callee). Users see inbox items for numbers on
their assignment list — the same per-user list that gates outbound sending —
and can view/download the decrypted PDF. Faxes whose callee isn't mirrored
in the portal are stored but visible to admins only (shown as "unmatched").
Delivery is idempotent on `(org, job_uuid)`, so gofaxserver retries never
duplicate an inbox entry. Retention is manual: admins can delete inbound
faxes; purging an org deletes its stored faxes too.

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

## Outbound status push (notify type `portal`)

The portal tracks outbound jobs primarily by polling gofaxserver
(`internal/poller` → `/fax/status`). To make final outcomes land instantly,
the portal also registers itself as a notify destination: every number's
derived notify string is

```
email_report->user1@x;user2@y,portal-><org svc_username>
```

(the `email_report` part only when users with emails are assigned; the
`portal->` part always). gofaxserver's notify dispatcher understands the
`portal` type: at job completion it POSTs a compact JSON payload — **no file
data** — to `<portal.url>/portal/api/notify/<svc_username>` with the same
`X-API-Key` pre-shared-key auth as inbound fax delivery:

```json
{
  "uuid": "3f7c1f2e-…", "success": true, "all_attempts_failed": false,
  "attempts": 2, "transferred_pages": 4,
  "result_text": "OK", "hangup_cause": "NORMAL_CLEARING",
  "caller_id_number": "+17785559876", "callee_number": "+16045551212",
  "start_ts": "…", "end_ts": "…"
}
```

The receiver (`handlers_notify.go`) flips the matching `FaxJob` to
`success`/`failed` (with pages, result text, completed-at) — but only while
the job is still non-terminal, so duplicate or late pushes never regress a
finished job. Unknown job UUIDs are 200 no-ops: inbound faxes and faxes
submitted outside the portal trigger the same notify string and must not
error. The push is fire-and-forget upstream (one POST, logged on failure) —
**the poller remains the source of truth** and converges anything a push
missed (portal down, network blip, `portal.url` unset).

Notes:

- The notify string lives on the gofaxserver number, so existing numbers pick
  up `portal->…` on their next portal-side update (assignment change, number
  edit, or re-adding the number).
- Requires `portal.url`/`portal.api_key` on gofaxserver and `inbound_api_key`
  on the portal — the same pair inbound delivery already uses.
- Mid-transmission events (page-by-page progress) are not pushed; the
  queued→sending transition still comes from the poller's active-faxes
  snapshot.

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

**Admin → Fax Policies** manages gofaxserver's T.38/ECM/V.17 policy rules
(`fax_policy_rules`): create/edit/delete manual rules scoped by dst, src, or
src/dst pair and by call type (`softmodem` vs `bridge`), inspect auto-learned
rules with their failure/success counters, expire a rule to reset it to
probing, dry-run policy resolution for any src/dst/call-type, and browse or
clear the persisted flip-flop pair states. See
[API_REFERENCE.md](API_REFERENCE.md#fax-policy-rules-t38--ecm--v17).

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
| `PORTAL_INBOUND_API_KEY` | optional pre-shared key for inbound fax delivery; must match gofaxserver `portal.api_key` |
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
6. Watch My Faxes flip `queued → sending → success/failed` — the final
   transition lands instantly via the `portal->` status push (the ~5 s poller
   is the fallback)
7. Confirm the `email_report` receipt lands in the user's inbox
8. Send a fax *to* the org's number — it should appear in the user's Inbox
   (and Admin → Inbound Faxes) and open as a PDF
9. Admin → Orgs → **Reconcile** should report a clean diff

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
`GET /portal/api/faxes/{id}`, plus the inbox: `GET /portal/api/inbox`,
`GET /portal/api/inbox/{id}`, `GET /portal/api/inbox/{id}/file` (decrypted
PDF, scoped to assigned numbers).

Inbound delivery (gofaxserver → portal, no session):
`POST /portal/api/inbound/{svc_username}` with optional `X-API-Key`.
Outbound status push (same auth): `POST /portal/api/notify/{svc_username}`.

Admin (`role=admin`): CRUD under `/portal/api/admin/{orgs,numbers,users,endpoints}`,
gateway provisioning under `/portal/api/admin/{gateways,gateway-templates}`
(including `gateways/adopt`, `gateways/unmanaged/{name}`, `gateways/{name}/repair`),
dialplan rules under `/portal/api/admin/dialplan`,
`PUT /portal/api/admin/numbers/{id}/assignments`, `GET /portal/api/admin/orgs/{id}/reconcile`,
`GET /portal/api/admin/faxes/active`, `GET /portal/api/admin/jobs[?org_id=&status=]`,
`GET /portal/api/admin/jobs/{id}/live`, `GET /portal/api/admin/inbox[?org_id=]`,
`GET /portal/api/admin/inbox/{id}/file`, `DELETE /portal/api/admin/inbox/{id}`,
`GET /portal/api/admin/audit`.

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
- Received fax PDFs are stored AES-256-GCM encrypted (domain-separated key
  derived from `encryption_key`) with plaintext SHA-256 integrity checks on
  every decryption; downloads are scoped to users assigned to the receiving
  number.

## Reconciliation

Because gofaxserver previously had no list APIs, drift was possible if people
edited it out-of-band. It now exposes read-only `GET /admin/tenants|numbers|
users|endpoints`; the portal's per-org **Reconcile** button diffs the mirror
against live state (missing tenants/numbers/users, ID mismatches). The rule:
**manage gofaxserver tenants through the portal**, treat CLI/curl edits as
exceptional, and re-run reconcile after them.

## Related Documentation

- [INSTALLATION.md](INSTALLATION.md) — Full installation guide (gofaxserver + FreeSWITCH + portal)
- [SETUP.md](SETUP.md) — Step-by-step setup procedure
- [GATEWAYS.md](GATEWAYS.md) — FreeSWITCH gateway configuration + API-driven provisioning
- [TENANTS.md](TENANTS.md) — Tenant, endpoint, and notify configuration
- [API_REFERENCE.md](API_REFERENCE.md) — gofaxserver API the portal proxies
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture

