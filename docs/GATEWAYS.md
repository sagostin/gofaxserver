# FreeSWITCH Gateway Configuration

This document details the configuration of FreeSWITCH gateways for connecting customer PBXes to the fax relay system.

## Gateway Files

Gateway configuration files are stored in:
```
/etc/freeswitch/gateways/
```

Template files are available in:
```
gofaxserver/examples/freeswitch/gateways/
```

## Gateway Template

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

### SBC Gateway (Carrier/Upstream Connection)

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
cp /etc/freeswitch/gateways/pbx_example.xml /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml
```

### 2. Edit the Gateway File

```bash
nano /etc/freeswitch/gateways/pbx_<CUSTOMERNAME>.xml
```

Update the following:
- `name` attribute: `pbx_<CUSTOMERNAME>`
- `realm` param: Customer's public IP or hostname

### 3. Load the Gateway

In FreeSwitch CLI (`fs_cli`):

```bash
sofia profile fax rescan
```

### 4. Verify Gateway Status

```bash
sofia status gateway pbx_<CUSTOMERNAME>
```

## FreeSwitch CLI Commands

### General

```bash
# Enter FreeSwitch CLI
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
2. The prefix used in the endpoint configuration API call

For example, if gateway file is `pbx_acme.xml` with `name="pbx_acme"`:
- API endpoint value: `pbx_acme:<PBX_IP>`
- This maps the gateway to a specific PBX IP address

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

## Common Issues

### Gateway Not Registering

1. Check `realm` is correct (IP or hostname)
2. Verify `register` is set appropriately
3. Check firewall allows SIP (port 5060) and RTP (ports 10000-20000)

### Calls Not Routing to Gateway

1. Verify gateway name matches in XML and API
2. Run `sofia profile fax rescan`
3. Check `sofia status gateway pbx_<NAME>`

### One-Way Audio

1. Enable RTP debugging: `sofia global siptrace on`
2. Check NAT settings
3. Verify RTP ports are open

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

## Related Documentation

- [SETUP.md](SETUP.md) - Step-by-step setup procedure
- [TENANTS.md](TENANTS.md) - Tenant and endpoint configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) - System architecture
- [API_REFERENCE.md](API_REFERENCE.md) - Full API documentation
