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
- `backup-all.sh` - Full system backup
- `backup-db.sh` - Database backup
- `backup-env.sh` - Environment configuration backup
- `backup-freeswitch.sh` - FreeSWITCH configuration backup
- `backup-gofaxserver-config.sh` - gofaxserver config backup
- `backup-ftp-upload.sh` - FTP upload utility
