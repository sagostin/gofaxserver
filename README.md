# gofaxserver

gofaxserver is a backend/connector providing Fax over IP support using FreeSWITCH and SpanDSP through FreeSWITCH's mod_spandsp.

In contrast to solutions like t38modem, iaxmodem and mod_spandsp's softmodem feature, gofaxserver does not emulate fax modem devices but replaces HylaFAX's `faxgetty` and `faxsend` processes to communicate directly with FreeSWITCH using FreeSWITCH's Event Socket interface.

gofaxserver is designed to provide a standalone fax server together with HylaFAX and a minimal FreeSWITCH setup; the necessary FreeSWITCH configuration is provided.

## Features

* SIP connectivity to PBXes, Media Gateways/SBCs and SIP Providers with or without registration
* Failover using multiple gateways
* Support for Fax over IP using T.38 **and/or** T.30 audio over G.711
* Native SIP endpoint, no modem emulation
* Support for an arbitrary number of lines (depending on the used hardware)
* Extensive logging and reporting using Loki and Postgres
* Fax receipts via Email, Webhook, and more (TBD)
* Send and Receive Faxes via Webhooks / APIs
* Add multiple endpoints for single numbers or tenants

## Components

gofaxserver consists of two commands that replace their native HylaFAX conterparts
* `gofaxsend` is used instead of HylaFAX' `faxsend `
* `gofaxd` is used instead of HylaFAX' `faxgetty`. Only one instance of `gofaxd` is necessary regardless of the number of receiving channels. 

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

Currently gofaxserver does not use HylaFAX configuration files *at all*. All configurations for both `gofaxd` and `gofaxsend` are made in the json-style configuration file `/etc/gofaxserver/config.conf` which has to be customized.

## Operation

### Starting

```
sudo systemctl restart freeswitch hylafax gofaxip hfaxd faxq
```

### Logging 

gofaxserver ogs everything it does to syslog & Loki. 

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
