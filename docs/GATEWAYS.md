# FreeSWITCH Gateway Configuration

This document details FreeSWITCH gateway configuration for connecting customer PBXes and upstream carriers to gofaxserver. It also covers the matching `gofaxserver` endpoint entries.

## Gateway Files

Gateway XML configuration files are stored in:
```
# Path A (all containers):     <repo>/volumes/gateways/   (mounted into both containers)
# Path B (FreeSWITCH on host): /etc/freeswitch/gateways/
```

Templates are available in:
```
gofaxserver/examples/freeswitch/gateways/
```

(These are reference samples only — `make fs-config` seeds
`volumes/freeswitch/gateways/` empty so the examples are never loaded by a
real FreeSWITCH instance.)

The `fax` Sofia profile (`examples/freeswitch/autoload_configs/sofia.conf.xml`) loads everything in that directory via:

```xml
<X-PRE-PROCESS cmd="include" data="../gateways/*.xml"/>
```

## Gateway Templates

### PBX Gateway (Customer Connection)

```xml
<!-- gofaxserver/examples/freeswitch/gateways/pbx_example.xml -->
<include>
    <gateway name="pbx_example">
        <param name="realm" value="pbx.example.com"/>
        <param name="extension" value="auto_to_user"/>
        <param name="caller-id-in-from" value="true"/>
        <param name="extension-in-contact" value="false"/>
        <param name="register" value="false"/>
    </gateway>
</include>
```

### SBC / Upstream Gateway (Carrier Connection)

```xml
<!-- gofaxserver/examples/freeswitch/gateways/sbc_example.xml -->
<include>
    <gateway name="sbc_example">
        <param name="realm" value="sbc.example.com"/>
        <param name="extension" value="auto_to_user"/>
        <param name="caller-id-in-from" value="true"/>
        <param name="extension-in-contact" value="true"/>
        <param name="register" value="false"/>
        <param name="ignore-early-media" value="true"/>
    </gateway>
</include>
```

SBC templates set `extension-in-contact=true` and `ignore-early-media=true` — both of which the gofaxserver routing code relies on (see [TENANTS.md](TENANTS.md) for T.38 logic).

## Gateway Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `name` | Gateway identifier (must match filename sans `.xml`) | Required |
| `realm` | Customer PBX host/IP or domain | Required |
| `extension` | How to route calls to this gateway | `auto_to_user` |
| `caller-id-in-from` | Use From header for caller ID | `true` |
| `extension-in-contact` | Use `extension@ip` format in Contact header | `false` |
| `register` | Whether to register with the gateway | `false` |
| `ignore-early-media` | Ignore early media from this gateway | `false` |

## Creating a New Gateway

Gateways can be created **manually** (below) or **via the API/portal** (next section) when provisioning is enabled.

### 1. Copy the Template

```bash
# Path A — from the repo checkout (mounted into the containers):
cp examples/freeswitch/gateways/pbx_example.xml volumes/gateways/pbx_<CUSTOMERNAME>.xml
# or for an upstream carrier:
cp examples/freeswitch/gateways/sbc_example.xml volumes/gateways/sbc_<CARRIERNAME>.xml

# Path B — FreeSWITCH on the host: same copies into /etc/freeswitch/gateways/
```

### 2. Edit the Gateway File

```bash
nano volumes/gateways/pbx_<CUSTOMERNAME>.xml        # Path A
# nano /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml   # Path B
```

Update the following:
- `name` attribute: `pbx_<CUSTOMERNAME>`
- `realm` param: Customer's public IP or hostname

### 3. Load the Gateway

In FreeSWITCH CLI (`fs_cli`, or `make fs-cli` / `docker exec -it freeswitch fs_cli` on Path A):

```bash
sofia profile fax rescan
```

### 4. Verify Gateway Status

```bash
sofia status gateway pbx_<CUSTOMERNAME>
```

## API-Driven Provisioning (Portal)

When `freeswitch.gateway_config_dir` is set in gofaxserver's `config.json`
(e.g. `/etc/freeswitch/gateways`, shared with the FreeSWITCH host/container
via mount), the whole manual flow above collapses into one call:

```bash
curl -X POST http://<FAX_SERVER>:8080/admin/gateway \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "pbx_acme",
    "template_id": 2,
    "params": { "realm": "198.51.100.20" },
    "type": "tenant",
    "type_id": 7,
    "priority": 0,
    "bridge": true
  }'
```

This renders the template, atomically writes `<gateway_config_dir>/pbx_acme.xml`,
runs `reloadxml` + `sofia profile fax rescan` over the event socket, and
creates the linked endpoint (`endpoint=pbx_acme:198.51.100.20` — the realm is
used as the ACL address unless `endpoint_ip` is given; a hostname realm works
too, see ACL Matching below). On any failure the file and
endpoint are rolled back.

Registered (SIP-auth) gateways are supported by the same templates:

```json
"params": { "realm": "sbc.carrier.com", "register": true, "username": "faxuser", "password": "s3cret" }
```

Other operations:

- `GET /admin/gateways` — list provisioned gateways with live `sofia status gateway` state, plus an `unmanaged` array of XML files on disk with no DB record (see below)
- `PUT /admin/gateway/{name}` — re-render/update (gateways cannot be renamed; delete and re-provision). The linked endpoint's scope, priority and bridge flag are preserved — only its `name:ip-or-hostname` value tracks realm/`endpoint_ip` changes, and the in-memory ACL (incl. DNS resolution) is reloaded immediately
- `DELETE /admin/gateway/{name}` — `sofia killgw`, remove XML, rescan, delete linked endpoint
- `POST /admin/gateway/{name}/repair` — re-render a managed gateway from its stored template+params (restores a file deleted or edited out-of-band)
- `POST /admin/gateways/from-endpoint` — bring an existing unmanaged endpoint under gateway management: `{endpoint_id, template_id, params}`. The endpoint value (`name:ip`) supplies the gateway name and default `realm`; the template is rendered, written, loaded, and a `GatewayConfig` row is linked to the existing endpoint (no new endpoint is created). Useful for imported databases where routing rows already exist
- `GET|POST /admin/gateway/templates`, `PUT|DELETE /admin/gateway/templates/{id}` — manage templates

### FreeSWITCH profile control

- `POST /admin/freeswitch/rescan` — `reloadxml` + `sofia profile <gateway_profile> rescan`. Safe, no call impact.
- `POST /admin/freeswitch/profile/restart?confirm=true` — full `sofia profile <gateway_profile> restart`. Drops all active calls on the profile; requires the confirm flag.

Both are also exposed in the portal admin UI (Gateways page).

### Sync between the database and disk

`GET /admin/gateways` reports drift in both directions:

- **Unmanaged files** — XML present in the gateway directory with no DB row
  (hand-created, or left behind). Each can be **adopted**
  (`POST /admin/gateways/adopt {file}`) — recorded as managed with no template
  until one is assigned on update — or **deleted**
  (`DELETE /admin/gateways/unmanaged/{name}?confirm=true`), which also tears
  the gateway down in FreeSWITCH.
- **Missing files** — managed gateways whose XML is gone show
  `exists_on_disk: false`; repair with `POST /admin/gateway/{name}/repair`.

Endpoints created by provisioning are protected: `PUT/DELETE
/admin/endpoint/{id}` on a gateway-linked endpoint is refused with a pointer
to the gateway API, so a gateway can't silently lose its endpoint row.

### Configuration

| Key (`freeswitch` section) | Default | Description |
|--------|---------|-------------|
| `gateway_config_dir` | *(empty = disabled)* | Directory gateway XML is written to; must be included by the sofia profile |
| `gateway_config_chown` | *(empty)* | `user:group` (names or uid:gid) to chown rendered files to; failures log a warning |
| `gateway_profile` | `fax` | Sofia profile hosting the gateways (used for rescan/killgw/status) |
| `gateway_monitor_seconds` | `60` | Poll interval for registration state of `register=true` gateways **and** for re-resolving hostname-valued gateway ACL entries; `-1` disables both |

The monitor polls `sofia status gateway` for registered gateways, records
`last_state` on the gateway row, and logs state transitions (drops at WARN,
recoveries at INFO). Each tick also re-resolves hostname ACL entries so
far-end IP changes are picked up automatically (last-good IPs kept on DNS
failure — see ACL Matching below). State is visible in `GET /admin/gateways`
and the portal.

Templates are stored in the database (seeded on first start from
`gofaxserver/templates/gateways/{sbc,pbx}.xml`, which mirror the
`examples/freeswitch/gateways/` files) and use Go template syntax —
`{{.realm}}`, `{{if .register}}...{{end}}`, etc. Declared variables are
exposed via the API so the portal renders a dynamic form per template.

In the portal: **Admin → Gateways** (provision/manage) and **Admin →
Templates** (edit templates). All actions are audit-logged.

## FreeSWITCH CLI Commands

### General

```bash
# Enter FreeSWITCH CLI
fs_cli                      # Path B (FreeSWITCH on host)
make fs-cli                 # Path A (= docker exec -it freeswitch fs_cli)

# Exit
/quit
```

### Gateway Management

```bash
# Rescan/reload gateways
sofia profile fax rescan

# Check all gateway status
sofia status

# Check specific gateway
sofia status gateway pbx_<NAME>

# Disable SIP tracing (reduce noise)
sofia global siptrace off

# Enable SIP tracing (debugging)
sofia global siptrace on
```

### Profile Management

```bash
# Restart a profile (reloads all gateways)
sofia profile fax restart

# Kill a specific gateway connection
sofia/killgw fax pbx_<NAME>
```

## Gateway Naming Convention

The gateway name in the XML file (`name="pbx_<CUSTOMERNAME>"`) must match:
1. The filename sans `.xml` extension
2. The prefix used in the `/admin/endpoint` API call

Allowed characters are letters (any case), digits and underscores — the name
must match `^[A-Za-z0-9_]+$`. Case is significant and preserved verbatim: the
XML filename, `<gateway name>` and endpoint prefix must use identical casing.

For example, if gateway file is `pbx_acme.xml` with `name="pbx_acme"`:
- API endpoint value: `pbx_acme:<PBX_IP>` (or `pbx_acme:<PBX_HOSTNAME>` — see ACL Matching below)
- This is what gofaxserver's `fsGatewayACL` uses to match the inbound source IP for ACL pass.

## Realm Configuration

The `realm` parameter specifies how FreeSWITCH identifies the remote SIP peer:

### IP Address
```xml
<param name="realm" value="192.168.1.100"/>
```

### Hostname
```xml
<param name="realm" value="pbx.customer.com"/>
```

### FQDN
```xml
<param name="realm" value="pbx.customer.example.com"/>
```

## ACL Matching (gofaxserver side)

gofaxserver matches inbound source IPs against the `endpoint` value of registered endpoints via `fsGatewayACL` (`gofaxserver/endpoints.go`).

For entries in the documented `xml_name:publicIP` format, the IP portion after the first `:` is compared **exactly** against the inbound source IP (legacy IP-only entries match on exact equality). Substring matching was removed — an entry for `192.168.1.10` no longer accidentally passes a call from `92.168.1.1`.

**Hostnames are supported.** An entry in `xml_name:hostname` form (e.g. produced automatically when the gateway's `realm` is a hostname and no `endpoint_ip` is given) is resolved via DNS, and inbound calls from **any** of its resolved A/AAAA records pass the ACL:

- Resolution happens when endpoints are (re)loaded and is **refreshed on every gateway monitor tick** (`freeswitch.gateway_monitor_seconds`, default 60s; a negative value disables both the registration monitor and the DNS refresh).
- When the far end's IP changes, the new IPs are picked up automatically on the next tick — no restart or re-provisioning needed. The change is logged at info level: `gateway ACL entry "..." resolved IPs changed: [...] -> [...]`.
- If a DNS lookup fails, the **last-good IPs are kept** (warning logged); a transient DNS outage never drops a gateway from the ACL. An entry that has never resolved fails closed (401).
- Updating a gateway via `PUT /admin/gateway/<name>` reloads endpoints and re-resolves immediately.

`GatewayEndpointsACL` is built from every endpoint with `endpoint_type=gateway` regardless of scope. This is why gateway endpoints must include the peer address in `xml_name:publicIP-or-hostname` format.

If the ACL fails, the inbound call is rejected with `respond 401` (`freeswitch_inbound.go`).

## Non-Register Mode

Most PBX connections use **non-register mode** (IP-based authentication):

```xml
<param name="register" value="false"/>
```

This means:
- No SIP REGISTER transaction
- Authentication based on source IP
- Caller ID passed in SIP headers

## Caller ID Handling

### From Header (Recommended)

```xml
<param name="caller-id-in-from" value="true"/>
```

Uses the caller ID from the From header, which most modern PBXes support.

### Diversion Header

For forwarded calls, the diversion header may contain the original caller:

```
Diversion: <sip:+15551234567@pbx.example.com>;privacy=off;reason=unconditional
```

To make gofaxserver parse the Diversion header for the called number instead of `sip_to_user`, set `faxing.recipient_from_diversion_header: true` in `config.json` (`freeswitch_inbound.go:168-178`).

## Extension Handling

### auto_to_user

```xml
<param name="extension" value="auto_to_user"/>
```

Automatically sets the extension to match the To header username.

### Fixed Extension

```xml
<param name="extension" value="fax"/>
```

Use a fixed extension for all calls through this gateway.

## Upstream / Global Endpoints

Upstream carrier gateways are represented in gofaxserver as endpoints with `type=global, type_id=0`. They are registered the same way as tenant endpoints, via `POST /admin/endpoint`:

```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "global",
    "type_id": 0,
    "endpoint_type": "gateway",
    "endpoint": "sbc_carrier1:198.51.100.10",
    "priority": 0
  }'
```

Global gateway endpoints are loaded into `Server.UpstreamFsGateways` and used as a fan-out fallback when no tenant/number endpoint matches. The Queue aggregates them into a single FreeSWITCH call (`isGlobalUpstreamGatewayGroup` in `queue.go:64-76`).

See [TENANTS.md](TENANTS.md) for the full priority/scope semantics.

## Common Issues

### Gateway Not Registering

1. Check `realm` is correct (IP or hostname)
2. Verify `register` is set appropriately
3. Check firewall allows SIP (port 5060) and RTP (ports 10000-20000)

### Calls Not Routing to Gateway

1. Verify gateway name matches in XML and the `/admin/endpoint` value
2. Run `sofia profile fax rescan`
3. Check `sofia status gateway pbx_<NAME>`
4. Confirm `fsGatewayACL` would pass for the source IP — the `endpoint` value must include the gateway's public IP or a hostname resolving to it

### ACL Failure (401 from gofaxserver)

If the inbound leg is failing with `respond 401`:

1. Check the gateway endpoint entry is registered (`/admin/reload` if recently added)
2. Verify the `endpoint` value is `xml_name:publicIP` with the exact public IP of the PBX/SBC after the colon (matching is exact, not substring) — or `xml_name:hostname`, in which case the source IP must be one of the hostname's currently resolved A/AAAA records
3. For hostname entries: confirm the hostname resolves from the gofaxserver host (`dig +short <hostname>`) and check the logs for `Gateway.Monitor` DNS warnings — an entry that has never resolved fails closed, while a resolved-then-failing entry keeps its last-good IPs
4. View the FreeSWITCH log to see the actual `sip_network_ip` variable for the call
5. Confirm `GatewayEndpointsACL` is populated (it is rebuilt on every `/admin/reload` and on endpoint create/update/delete)

### One-Way Audio

1. Enable RTP debugging: `sofia global siptrace on`
2. Check NAT settings
3. Verify RTP ports are open
4. Confirm the Sofia `fax` profile's `local-network-acl` matches your network (`sofia.conf.xml:35`)

## Example: Complete Gateway Configuration

```xml
<include>
    <gateway name="pbx_acme">
        <!-- Customer PBX identification -->
        <param name="realm" value="192.168.1.100"/>

        <!-- Routing -->
        <param name="extension" value="auto_to_user"/>

        <!-- Caller ID -->
        <param name="caller-id-in-from" value="true"/>

        <!-- Contact format -->
        <param name="extension-in-contact" value="false"/>

        <!-- Authentication -->
        <param name="register" value="false"/>

        <!-- Media -->
        <param name="ignore-early-media" value="true"/>
    </gateway>
</include>
```

And the matching gofaxserver endpoint:

```bash
curl -X POST http://<FAX_SERVER>:8080/admin/endpoint \
  -H "Authorization: Basic $(echo -n 'admin:<API_KEY>' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "tenant",
    "type_id": 1,
    "endpoint_type": "gateway",
    "endpoint": "pbx_acme:192.168.1.100",
    "priority": 0
  }'
```

## Related Documentation

- [INSTALLATION.md](INSTALLATION.md) — Full installation guide
- [SETUP.md](SETUP.md) — Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) — Tenant and endpoint configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
- [API_REFERENCE.md](API_REFERENCE.md) — Full API documentation
- [PORTAL.md](PORTAL.md) — Web portal (see its runbook for onboarding customers with PBX gateways)
