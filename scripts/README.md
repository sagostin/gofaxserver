# Scripts

This directory contains utility scripts for managing the fax server.

## Setup Scripts

### `setup-tenant-tui.py`

Interactive Python TUI for creating new tenants.

**Requirements:**
```bash
pip install prompt_toolkit requests
```

**Usage:**
```bash
export FAX_API_URL=http://<fax_server>:8080
export FAX_API_KEY=<your_api_key>
python setup-tenant-tui.py
```

**Features:**
- Step-by-step wizard for tenant creation
- Bridge/Relay mode selection
- Multi-number support
- Optional notification settings
- API error handling

**Note:** FreeSWITCH gateway configuration must be done manually before using this tool.

## Backup Scripts

See individual script files for backup utilities:
- `backup-all.sh` - Full runtime backup for gofaxserver and, when configured, the portal
- `backup-db.sh` - GoFaxServer database backup
- `backup-env.sh` - GoFaxServer root environment backup
- `backup-freeswitch.sh` - FreeSWITCH configuration and generated gateway backup
- `backup-gofaxserver-config.sh` - GoFaxServer config backup
- `backup-portal-db.sh` - Portal database backup (includes branding and stored faxes)
- `backup-portal-env.sh` - Portal environment and encryption-secret backup
- `backup-portal-config.sh` - Optional portal config backup
- `backup-ftp-upload.sh` - FTP upload utility, including hidden `.env` snapshots
