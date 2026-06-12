# FreeSWITCH Gateway Configuration

This document details FreeSWITCH gateway configuration for connecting customer PBXes and upstream carriers to gofaxserver. It also covers the matching `gofaxserver` endpoint entries.

## Gateway Files

Gateway XML configuration files are stored in:
```
/etc/freeswitch/gateways/
```

Templates are available in:
```
gofaxserver/examples/freeswitch/gateways/
```

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

### 1. Copy the Template

```bash
cp examples/freeswitch/gateways/pbx_example.xml /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml
# or for an upstream carrier:
cp examples/freeswitch/gateways/sbc_example.xml /etc/freeswitch/gateways/sbc_<CARRIERNAME>.xml
```

### 2. Edit the Gateway File

```bash
nano /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml
```

Update the following:
- `name` attribute: `pbx_<CUSTOMERNAME>`
- `realm` param: Customer's public IP or hostname

### 3. Load the Gateway

In FreeSWITCH CLI (`fs_cli`):

```bash
sofia profile fax rescan
```

### 4. Verify Gateway Status

```bash
sofia status gateway pbx_<CUSTOMERNAME>
```

## FreeSWITCH CLI Commands

### General

```bash
# Enter FreeSWITCH CLI
fs_cli

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

For example, if gateway file is `pbx_acme.xml` with `name="pbx_acme"`:
- API endpoint value: `pbx_acme:<PBX_IP>`
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

gofaxserver matches inbound source IPs against the `endpoint` value of registered endpoints via `fsGatewayACL` (`gofaxserver/endpoints.go:235-243`):

```go
func (s *Server) fsGatewayACL(ip string) (string, error) {
    for _, k := range s.GatewayEndpointsACL {
        if strings.Contains(k, ip) {
            return k, nil
        }
    }
    return "", errors.New("unable to find matching gateway from sending IP")
}
```

`GatewayEndpointsACL` is built from every endpoint with `endpoint_type=gateway` regardless of scope (`endpoints.go:60-62`). For the ACL to pass, the inbound source IP must appear as a substring of one of the registered endpoint strings — which is why gateway endpoints must include the public IP in `xml_name:publicIP` format.

If the ACL fails, the inbound call is rejected with `respond 401` (`freeswitch_inbound.go:180-192`).

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
4. Confirm `fsGatewayACL` would pass for the source IP — the `endpoint` value must include the gateway's public IP

### ACL Failure (401 from gofaxserver)

If the inbound leg is failing with `respond 401`:

1. Check the gateway endpoint entry is registered (`/admin/reload` if recently added)
2. Verify the `endpoint` value contains the public IP of the PBX/SBC
3. View the FreeSWITCH log to see the actual `sip_network_ip` variable for the call
4. Confirm `GatewayEndpointsACL` is populated (it is rebuilt on every `/admin/reload` and on endpoint create/update/delete)

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

- [SETUP.md](SETUP.md) — Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) — Tenant and endpoint configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture
- [API_REFERENCE.md](API_REFERENCE.md) — Full API documentation
