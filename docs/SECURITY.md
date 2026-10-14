# SIP Brute-Force Sentry (Dynamic IP Banning)

gofaxserver enforces inbound authorization at the application layer: every
INVITE reaches the dialplan socket and is matched against the gateway
endpoint ACLs (`fsGatewayACL`). That keeps unauthorized calls out, but a
scanner hammering the SIP port still costs a FreeSWITCH channel, an event
socket connection, and log lines (locally and in Loki) per attempt.

The **security sentry** closes that gap: repeat ACL failures from the same
source IP are banned at the **host firewall**, so packets are dropped in the
kernel before FreeSWITCH ever sees them.

## How it works

1. Each inbound call that fails the gateway ACL check is recorded against
   its source IP (sliding window, in memory).
2. When an IP exceeds `fail_threshold` failures within `fail_window`, it is
   banned via the configured firewall backend and persisted in the
   `blocked_ips` table (bans survive restarts — they are re-applied at boot).
3. Ban duration escalates per offense using `ban_durations`; an IP banned
   more times than there are entries is banned **permanently**.
4. Hits from already-banned IPs are answered `403` and logged at most once
   per IP per 5 minutes — no log/Loki spam even in `log-only` mode.
5. A janitor (every `janitor_seconds`) expires timed-out bans, re-asserts
   firewall rules that went missing (firewall flush, earlier error), and
   **reconciles** bans against the gateway ACLs.

### Automatic unbanning

A banned IP is released automatically when it becomes legitimate:

- an endpoint is added/updated whose gateway ACL entry contains the IP
  (hooks in endpoint loading), or
- a hostname-valued ACL entry re-resolves onto the banned IP (gateway
  monitor refresh), or
- the IP is added to `whitelist_cidrs`.

IPs matching a gateway ACL or whitelist are also **never banned** in the
first place, so a misconfigured customer PBX cannot lock itself out beyond
the current ACL failure behavior.

## Configuration (`config.json`)

```json
"security": {
  "enabled": false,
  "backend": "nft",
  "fail_threshold": 5,
  "fail_window": "60s",
  "ban_durations": ["10m", "1h", "24h"],
  "whitelist_cidrs": ["203.0.113.0/24", "198.51.100.7"],
  "janitor_seconds": 60
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Master switch. Off = no tracking, no bans. |
| `backend` | `nft` | Enforcement: `nft`, `ipset`, or `log-only` (detect + suppress logs, no firewall changes — use this first to validate thresholds). |
| `fail_threshold` | `5` | Failed ACL attempts within the window that trigger a ban. |
| `fail_window` | `60s` | Sliding window for counting failures. |
| `ban_durations` | `10m, 1h, 24h` | Escalating durations per offense; beyond the last entry = permanent. |
| `whitelist_cidrs` | `[]` | Networks/IPs that are never banned. Bare IPs allowed. |
| `janitor_seconds` | `60` | Expiry + firewall re-sync + ACL reconcile interval. |

## Firewall privileges

The sentry shells out to `nft`/`ipset`, which need `CAP_NET_ADMIN`.

- **Docker** (host networking — both compose files): the image installs
  `nftables`/`ipset` and setcaps the binaries for the non-root user; the
  compose files grant `cap_add: NET_ADMIN`. Nothing else to do.
- **Host (deb/systemd)**: install `nftables` (or `ipset`) and uncomment
  `AmbientCapabilities=CAP_NET_ADMIN` in `/etc/systemd/system/gofaxserver.service`
  (see `debian/service`), then `systemctl daemon-reload && systemctl restart gofaxserver`.

If firewall commands fail (missing capability/package), the sentry keeps
working in a degraded mode: bans are recorded in Postgres, log suppression
still applies, and the janitor retries enforcement every interval
(`enforce_errors` metric counts these).

### nftables backend details

Creates a dedicated `inet gofax_sentry` table with `blocklist4`/`blocklist6`
timeout sets and an input chain hooked at `priority filter - 10` — ahead of
ufw and other filter-priority chains. Coexists with ufw/firewalld; remove
with `nft delete table inet gofax_sentry` after disabling the feature.

### ipset backend details

Creates `gofax_block` (`hash:ip` with per-element timeouts) and inserts a
single `-m set --match-set gofax_block src -j DROP` rule at the top of
`INPUT`.

## Admin API

All under `/admin` (Basic auth with `web.api_key`):

- `GET /admin/security/status` — enabled flag, backend, thresholds, counters.
- `GET /admin/security/blocked-ips` — active bans; `?all=true` includes history.
- `POST /admin/security/blocked-ips` — manual ban; body
  `{"ip": "203.0.113.10", "reason": "scanner", "duration": "24h"}`
  (empty `duration` = permanent).
- `DELETE /admin/security/blocked-ips/{ip}` — manual unban.

The portal exposes the same operations (audited) under
**Admin → Security**, with live metrics (active bans, blocked hits,
auto-unbans, firewall errors) and a ban/unban UI.

## Operational notes

- **Rollout**: start with `backend: "log-only"` to observe what *would* be
  banned against real traffic, then switch to `nft`.
- **False positives**: a customer typo'ing credentials gets at most the
  first-rung duration (10m default), and is freed instantly once their IP is
  added as an endpoint.
- **Distributed scanners**: per-IP banning does not stop attacks that rotate
  source IPs every attempt; the log suppression fast-path still bounds the
  log volume in that case.
