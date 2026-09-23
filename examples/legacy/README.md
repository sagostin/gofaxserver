# Legacy — Not Used by gofaxserver

This directory contains configuration files inherited from the original
[GOfax.IP](https://github.com/gonicus/gofaxip) project, which gofaxserver was
forked from. **gofaxserver does not use any of these files.**

## Contents

- `gofax.conf` — HylaFAX `.conf` format (INI) consumed by `gofaxd`/`gofaxsend`
  daemons. Replaced by `/etc/gofaxserver/config.json` (JSON, parsed by
  `gofaxlib/config.go`).
- `hylafax/` — HylaFAX templates (`DynamicConfig`, `FaxDispatch`, `FaxNotify`).
  HylaFAX is no longer a runtime dependency of gofaxserver; the only outbound
  queue is now `gofaxserver/queue.go` writing to the database and to
  FreeSWITCH via ESL.

## Removal

These files are kept only for historical reference. They can be deleted at
any time:

```bash
rm -rf examples/legacy
```

The current FreeSWITCH configuration templates that **are** still in use live
in `examples/freeswitch/` and are documented in
[`../docs/GATEWAYS.md`](../../docs/GATEWAYS.md).
