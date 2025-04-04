# gofaxserver

gofaxserver is a backend/connector providing Fax over IP support using FreeSWITCH and SpanDSP through FreeSWITCH's mod_spandsp.

In contrast to solutions like t38modem, iaxmodem and mod_spandsp's softmodem feature, gofaxserver does not emulate fax modem devices but replaces HylaFAX's `faxgetty` and `faxsend` processes to communicate directly with FreeSWITCH using FreeSWITCH's Event Socket interface.

gofaxserver is designed to provide a standalone fax server together with HylaFAX and a minimal FreeSWITCH setup; the necessary FreeSWITCH configuration is provided.

## Features

* Multi-tenant architecture with isolated tenant management
* RESTful API for fax management, tenant administration, and user authentication
* SIP connectivity to PBXes, Media Gateways/SBCs and SIP Providers with or without registration
* Failover using multiple gateways with priority-based routing
* Support for Fax over IP using T.38 **and/or** T.30 audio over G.711
* Native SIP endpoint, no modem emulation
* Support for an arbitrary number of lines (depending on the used hardware)
* Extensive logging and reporting using Loki and PostgreSQL
* Fax receipts via Email, Webhook, and customizable notification methods
* Send and Receive Faxes via RESTful API
* Dynamic endpoint management for numbers and tenants
* Automatic fallback from T.38 to SpanDSP softmodem for problematic remote stations

## Components

gofaxserver consists of two commands that replace their native HylaFAX counterparts:
* `gofaxsend` is used instead of HylaFAX' `faxsend`
* `gofaxd` is used instead of HylaFAX' `faxgetty`. Only one instance of `gofaxd` is necessary regardless of the number of receiving channels.

## Architecture

gofaxserver is built with a multi-tenant architecture that allows for isolated management of fax resources. The system is composed of the following key components:

### Core Components

1. **Event Socket Server**: Handles communication with FreeSWITCH for inbound and outbound fax operations
2. **Router**: Routes incoming fax calls to the appropriate tenant based on the dialed number
3. **Queue**: Manages outbound fax jobs and retries
4. **Web Server**: Provides the RESTful API for administration and tenant operations

### Data Models

#### Tenant
A tenant represents an organization or department that uses the fax system.

```json
{
  "id": 1,
  "name": "Acme Corp",
  "email": "contact@acme.com"
}
```

#### TenantNumber
A phone number assigned to a tenant for sending and receiving faxes.

```json
{
  "id": 1,
  "tenant_id": 1,
  "number": "5551234567",
  "notify_emails": "notify@acme.com",
  "cid": "+15551234567",
  "webhook": "Acme Fax Relay"
}
```

#### TenantUser
A user account associated with a tenant that can authenticate and send faxes.

```json
{
  "id": 1,
  "tenant_id": 1,
  "username": "tenantuser",
  "password": "encrypted_password",
  "api_key": "user_api_key"
}
```

#### Endpoint
A connection point for sending or receiving faxes, which can be associated with a tenant or a specific number.

```json
{
  "id": 1,
  "type": "tenant",         // "tenant", "number", or "global"
  "type_id": 1,             // For tenant endpoints, this is the tenant ID; for number endpoints, it's the TenantNumber ID
  "endpoint_type": "gateway", // e.g. "gateway", "webhook", "email"
  "endpoint": "pbx_acme",   // For gateways, the FreeSWITCH gateway name (or a webhook URL, etc.)
  "priority": 0
}
```

### Notification System

gofaxserver includes a flexible notification system that can send fax receipts and status updates via:

1. **Email**: Using the configured SMTP server
2. **Webhooks**: HTTP callbacks to external systems
3. **Custom endpoints**: Extensible for additional notification methods

## Installation

We recommend running gofaxserver on Debian 12 ("bookworm"), so these instructions cover Debian in detail. Of course it is possible to install and use gofaxserver on other Linux distributions and possibly other Unixes supported by golang, FreeSWITCH and HylaFAX.

Due to https://bugs.debian.org/cgi-bin/bugreport.cgi?bug=1076953 the current Hylafax Packages in 12 will stop working after executing the provided cronjobs `/etc/cron.weekly/hylafax` and `/etc/cron.monthly/hylafax`.
You can use `debian12_workaround.sh` from this repository to workaround that issue. This script has to be executed once after installing Hylafax packages.

### Dependencies

The official FreeSWITCH Debian repository can be used to obtain and install all required FreeSWITCH packages. To access the Repo, you first need to create a Signalwire API Token. Follow this guide: https://freeswitch.org/confluence/display/FREESWITCH/HOWTO+Create+a+SignalWire+Personal+Access+Token


After you created the api token you can continue with adding the repository

```
TOKEN=YOURSIGNALWIRETOKEN

apt-get update && apt-get install -y gnupg2 wget lsb-release

wget --http-user=signalwire --http-password=$TOKEN -O /usr/share/keyrings/signalwire-freeswitch-repo.gpg https://freeswitch.signalwire.com/repo/deb/debian-release/signalwire-freeswitch-repo.gpg

echo "machine freeswitch.signalwire.com login signalwire password $TOKEN" > /etc/apt/auth.conf
echo "deb [signed-by=/usr/share/keyrings/signalwire-freeswitch-repo.gpg] https://freeswitch.signalwire.com/repo/deb/debian-release/ `lsb_release -sc` main" > /etc/apt/sources.list.d/freeswitch.list
echo "deb-src [signed-by=/usr/share/keyrings/signalwire-freeswitch-repo.gpg] https://freeswitch.signalwire.com/repo/deb/debian-release/ `lsb_release -sc` main" >> /etc/apt/sources.list.d/freeswitch.list

apt-get update
```

### Installing packages

```
apt-get update
apt-get install freeswitch freeswitch-mod-commands freeswitch-mod-dptools freeswitch-mod-event-socket freeswitch-mod-sofia freeswitch-mod-spandsp freeswitch-mod-tone-stream freeswitch-mod-db freeswitch-mod-syslog freeswitch-mod-logfile
```

### gofaxserver (TODO)

See [releases](https://github.com/gonicus/gofaxip/releases) for amd64 Debian packages.

Use ```dpkg -i``` to install the latest package.

See [Building](#building) for instructions on how to build the binaries or Debian packages from source.
This is only necessary if you want to use the latest/untested version or if you need another architecture than amd64!

## Configuration

### FreeSWITCH

FreeSWITCH has to be able to place received faxes in HylaFAX' `recvq` spool. The simplest way to achieve this is to run FreeSWITCH as the `uucp` user. 

```
sudo chown -R uucp:uucp /var/log/freeswitch
sudo chown -R uucp:uucp /var/lib/freeswitch
sudo cp /usr/share/doc/gofaxip/examples/freeswitch.service /etc/systemd/system/
sudo systemctl daemon-reload
```

A very minimal FreeSWITCH configuration for gofaxserver is provided in the repository.

```
sudo cp -r /usr/share/doc/gofaxip/examples/freeswitch/* /etc/freeswitch/
```

The SIP gateway to use has to be configured in `/etc/freeswitch/gateways/sbc_example.xml`. It is possible to configure multiple gateways for gofaxserver.

**Depending on your installation/SIP provider you have to either:**
 * edit `/etc/freeswitch/vars.xml` and adapt the IP address FreeSWITCH should use for SIP/RTP.
 * remove the entire section concerning the `sofia_ip` variable (parameters `sip-port, rtp-ip, sip-ip, ext-rtp-ip, ext-sip-ip`)

### gofaxserver

Currently gofaxserver does not use HylaFAX configuration files *at all*. All configurations for both `gofaxd` and `gofaxsend` are made in the json-style configuration file `/etc/gofaxserver/config.json` which has to be customized.

#### Configuration File Structure

The configuration file (`/etc/gofaxserver/config.json`) contains the following main sections:

```json
{
  "freeswitch": {
    "event_client_socket": "127.0.0.1:8021",
    "event_client_socket_password": "ClueCon",
    "event_server_socket": "127.0.0.1:8084",
    "ident": "gofaxserver",
    "header": "GOfax.IP Fax Server",
    "verbose": true,
    "softmodem_fallback": true
  },
  "faxing": {
    "temp_dir": "/tmp",
    "enable_t38": true,
    "request_t38": true,
    "recipient_from_diversion_header": false,
    "answer_after": 2,
    "wait_time": 10,
    "disable_v17_after_retry": "3",
    "disable_ecm_after_retry": "2",
    "failed_response": ["USER_BUSY", "NO_ANSWER", "NO_USER_RESPONSE", "CALL_REJECTED"],
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
  "loki": {
    "push_url": "http://localhost:3100/loki/api/v1/push",
    "username": "loki_user",
    "password": "loki_password",
    "job": "faxserver"
  },
  "web": {
    "listen": ":8080",
    "api_key": "your_api_key"
  },
  "smtp": {
    "host": "smtp.example.com",
    "port": 587,
    "username": "smtp_user",
    "password": "smtp_password",
    "encryption": "tls",
    "from_address": "fax@example.com",
    "from_name": "Fax Server"
  },
  "psk": "your_encryption_key"
}
```

#### Database Setup

gofaxserver requires a PostgreSQL database. Create a database and user with the following commands:

```sql
CREATE DATABASE gofaxserver;
CREATE USER gofaxserver WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE gofaxserver TO gofaxserver;
```

The database schema will be automatically created when gofaxserver starts for the first time.

## Operation

### Starting

```
sudo systemctl restart freeswitch hylafax gofaxip hfaxd faxq
```

### Logging 

gofaxserver logs everything it does to syslog & Loki.

### API Usage

gofaxserver provides a comprehensive RESTful API for managing tenants, numbers, endpoints, and fax operations. The API is divided into two main sections:

1. **Admin API** - For system administrators to manage tenants, numbers, and endpoints
2. **Tenant API** - For tenant users to authenticate and send faxes

#### Authentication

- **Admin API**: Uses Basic Authentication with the API key configured in the `web.api_key` setting
- **Tenant API**: Uses Basic Authentication with tenant username and password

#### Admin API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/admin/reload` | GET | Reload configuration data |
| `/admin/tenant` | POST | Add a new tenant |
| `/admin/tenant/{id}` | PUT | Update an existing tenant |
| `/admin/tenant/{id}` | DELETE | Delete a tenant |
| `/admin/number` | POST | Add a tenant number |
| `/admin/number/{id}` | PUT | Update a tenant number |
| `/admin/number` | DELETE | Delete a tenant number |
| `/admin/endpoint` | POST | Add an endpoint |
| `/admin/endpoint/{id}` | PUT | Update an endpoint |
| `/admin/endpoint/{id}` | DELETE | Delete an endpoint |
| `/admin/user` | POST | Add a tenant user |
| `/admin/user/{id}` | PUT | Update a tenant user |
| `/admin/user/{id}` | DELETE | Delete a tenant user |

#### Tenant API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/fax/send` | POST | Send a fax document |
| `/fax/status` | GET | Check the status of a fax job |

#### Example: Sending a Fax

```bash
curl -X POST http://localhost:8080/fax/send \
  -H "Authorization: Basic $(echo -n 'username:password' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "from_number": "5551234567",
    "to_number": "5559876543",
    "file_name": "/path/to/document.tiff"
  }'
```

#### Example: Checking Fax Status

```bash
curl -X GET "http://localhost:8080/fax/status?uuid=43ef7a37-2a03-430d-a686-49ec5a38540c" \
  -H "Authorization: Basic $(echo -n 'username:password' | base64)"
```

For more detailed API documentation, see [README_API.md](README_API.md).

### Fallback from T.38 to SpanDSP softmodem

In rare cases we noticed problems with certain remote stations that could not successfully work with some T.38 Gateways we tested. In the case we observed, the remote tried to use T.4 1-D compression with ECM enabled. After disabling T.38 the fax was successfully received. 

To work around this rare sort of problem and improve compatiblity, gofaxserver can identify failed transmissions and dynamically disable T.38 for the affected remote station and have FreeSWITCH use SpanDSP's pure software fax implementation. The station is identified by caller id and saved in FreeSWITCH's `mod_db`.
To enable this feature, set `softmodemfallback = true` in `config.json`.

Note that this will only affect all subsequent calls from/to the remote station, assuming that the remote station will retry a failed fax. Entries in the fallback list are persistant and will not be expired by gofaxserver. It is possible however to manually expire entries from mod_db. The used `<realm>/<key>/<value>` is `fallback/<callerid>/<unix_timestamp>`, with unix_timestamp being the time when the entry was added. See https://freeswitch.org/confluence/display/FREESWITCH/mod_db for details on mod_db. 

A transmission is regarded as failed and added to the fallback database if SpanDSP reports the transmission as not successful and one of the following conditions apply:

* Negotiation has happened multiple times
* Negotiation was successful but transmitted pages contain bad rows
* 
### Override transmission parameters for individual destinations

It is possible to override transmission parameters for individual destinations by inserting entries to FreeSWITCHs internal database (mod_db).
Before originating the transmission, `gofaxsend` checks if a matching database entry exists. The realm is *override-$destination*, where $destination is the target number.
The found keys are used as parameters for the outgoing channel of FreeSWITCH.

Example:

Disable T.38 transmission for destination 012345:
```
fs_cli -x 'db insert/override-012345/fax_enable_t38/false'
```

See https://freeswitch.org/confluence/display/FREESWITCH/mod_spandsp#mod_spandsp-Controllingtheapp for mod_spandsp parameters.

See https://freeswitch.org/confluence/display/FREESWITCH/mod_db for a reference on how to use mod_db.

# Building

gofaxserver is implemented in [Go](https://golang.org/doc/install), it can be built using `go get`.

```
go get github.com/sagostin/gofaxserver/...
```

This will produce the binaries `gofaxd` and `gofaxsend`.

## Build debian package

You need golang and dh-golang from jessie-backports.

With golang package from debian repository:
```
apt update
apt install git dh-golang dh-systemd golang
git clone https://github.com/gonicus/gofaxip
cd gofaxip
dpkg-buildpackage -us -uc -rfakeroot -b
```

If you dont have golang from debian repository installed use ```-d``` to ignore builddeps.
