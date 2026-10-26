# GoFaxServer — Backup & Restore Guide

## Overview

The `scripts/` directory contains nine backup scripts:

| Script | Purpose |
|---|---|
| `backup-db.sh` | Dump the gofaxserver PostgreSQL database (gzipped) |
| `backup-env.sh` | Snapshot the gofaxserver root `.env` file |
| `backup-freeswitch.sh` | Archive FreeSWITCH configuration and generated gateways |
| `backup-gofaxserver-config.sh` | Snapshot gofaxserver `config.json` |
| `backup-portal-db.sh` | Dump the portal PostgreSQL database (gzipped) |
| `backup-portal-env.sh` | Snapshot `portal/.env` |
| `backup-portal-config.sh` | Snapshot optional `portal/config.json` |
| `backup-ftp-upload.sh` | Upload backup files, including hidden `.env` snapshots, to FTP |
| `backup-all.sh` | Runs all applicable backups in sequence, then uploads them |

All backups are saved to `./backups/<type>/` by default, with timestamped filenames.

The gofaxserver database dump covers all gofaxserver tables, including
`gateway_templates`, `gateway_configs`, and `dialplan_rules`. Rendered gateway
XML is covered by the FreeSWITCH archive. When `portal/.env` or
`portal/config.json` exists, `backup-all.sh` also backs up the separate portal
database and runtime configuration. The portal DB includes orgs, users, job
history, audit logs, branding (including logo/favicon images), and
AES-256-GCM-sealed inbound faxes. Fax-only installations skip the portal steps.

> **All-container installs (Path A):** the backup scripts prefer
> `/etc/freeswitch` and `/etc/gofaxserver/config.json`, then fall back to
> `volumes/freeswitch/` and `config.json`. The FreeSWITCH archive also includes
> `volumes/gateways/` when present. Explicit `FS_CONFIG_DIR` and
> `GOFAXSERVER_CONFIG` overrides take precedence. The pristine templates in
> `examples/freeswitch/` are in git and need no backup.

> **Do not back up `faxing.temp_dir`** (default `/var/lib/gofaxserver/tmp`).
> It holds transient store-and-forward fax content — sensitive and
> self-cleaning (per-job deletion + a periodic janitor). Excluding it keeps
> backups smaller and avoids retaining fax content beyond its intended
> lifetime.

---

## 1. Configuration

Add these to your `.env` (see `sample.env` for reference):

```env
# Backup retention (optional, default 30 days for DB/FS, 90 for .env)
BACKUP_KEEP_DAYS=30

# FTP upload (optional — omit to skip FTP entirely)
FTP_HOST=ftp.example.com
FTP_USER=backupuser
FTP_PASS=backuppassword
FTP_PORT=21
FTP_REMOTE_DIR=/backups
```

> **Note:** If any of `FTP_HOST`, `FTP_USER`, or `FTP_PASS` is unset, the FTP step is skipped with a message and is not treated as a failure. All local backup steps still run.

### Which env vars are read by which script

| Script | Reads |
|--------|-------|
| `backup-db.sh` | `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_PORT`, `BACKUP_KEEP_DAYS` |
| `backup-env.sh` | `BACKUP_KEEP_DAYS` |
| `backup-freeswitch.sh` | `FS_CONFIG_DIR`, `BACKUP_KEEP_DAYS` |
| `backup-gofaxserver-config.sh` | `GOFAXSERVER_CONFIG`, `BACKUP_KEEP_DAYS` |
| `backup-portal-db.sh` | `PORTAL_DB_NAME`, `PORTAL_DB_USER`, `PORTAL_DB_PASSWORD`, `PORTAL_DB_HOST`, `PORTAL_DB_PORT`, `BACKUP_KEEP_DAYS` |
| `backup-portal-env.sh` | `PORTAL_ENV_FILE`, `BACKUP_KEEP_DAYS` |
| `backup-portal-config.sh` | `PORTAL_CONFIG`, `BACKUP_KEEP_DAYS` |
| `backup-ftp-upload.sh` | `FTP_HOST`, `FTP_USER`, `FTP_PASS`, `FTP_PORT`, `FTP_REMOTE_DIR` (all optional) |
| `backup-all.sh` | All of the above (chained); portal steps are conditional |

---

## 2. Running Backups Manually

### Full backup (all applicable components + FTP upload)

```bash
sudo ./scripts/backup-all.sh
```

Portal steps run automatically when `portal/.env` or `portal/config.json`
exists; fax-only installations skip them.

### Individual backups

```bash
./scripts/backup-db.sh                       # gofaxserver database only
./scripts/backup-env.sh                      # gofaxserver root .env only
sudo ./scripts/backup-freeswitch.sh          # FreeSWITCH + generated gateways
sudo ./scripts/backup-gofaxserver-config.sh  # gofaxserver config only
./scripts/backup-portal-db.sh                # portal database only
./scripts/backup-portal-env.sh               # portal .env only
./scripts/backup-portal-config.sh            # optional portal config only
```

### Custom backup directory

Every script accepts an optional path argument:

```bash
./scripts/backup-db.sh /mnt/nfs/backups/db
sudo ./scripts/backup-all.sh /mnt/external/gofax-backups
```

### Standalone FTP upload

```bash
./scripts/backup-ftp-upload.sh ./backups/db db
```

---

## 3. Automating with Cron

### Daily full backup at 3:00 AM

```bash
sudo crontab -e
```

Add:

```cron
0 3 * * * cd /opt/gofaxserver && ./scripts/backup-all.sh >> /var/log/gofax-backup.log 2>&1
```

> **Tip:** Use `sudo crontab -e` (not `crontab -e`) when your project lives in a root-owned path or your `.env`/config files require root to read.

### Database-only backup every 6 hours

```cron
0 */6 * * * cd /opt/gofaxserver && ./scripts/backup-db.sh >> /var/log/gofax-backup-db.log 2>&1
```

> **Tip:** Replace `/opt/gofaxserver` with wherever your project lives on the server.

### Verify cron is working

```bash
# Check the log after the scheduled time
tail -50 /var/log/gofax-backup.log

# List what's been backed up
ls -lhR ./backups/
```

---

## 4. Backup Retention

Old backups are automatically pruned each time a backup runs:

| Type | Default Retention | Override |
|---|---|---|
| GoFaxServer database (`.sql.gz`) | 30 days | `BACKUP_KEEP_DAYS` |
| GoFaxServer `.env` snapshots | 90 days | `BACKUP_KEEP_DAYS` |
| FreeSWITCH (`.tar.gz`) | 30 days | `BACKUP_KEEP_DAYS` |
| GoFaxServer config (`config.json`) | 30 days | `BACKUP_KEEP_DAYS` |
| Portal database (`.sql.gz`) | 30 days | `BACKUP_KEEP_DAYS` |
| Portal `.env` snapshots | 90 days | `BACKUP_KEEP_DAYS` |
| Portal config (`config.json`) | 30 days | `BACKUP_KEEP_DAYS` |

---

## 5. Restore Procedures

### 5a. Restore PostgreSQL Database

```bash
# Locate the backup
ls -lh ./backups/db/

# Restore (replace the filename with your target backup)
gunzip -c ./backups/db/fax_20260317_030000.sql.gz | \
  PGPASSWORD="$POSTGRES_PASSWORD" psql \
    -h localhost -p 5432 \
    -U "$POSTGRES_USER" \
    -d "$POSTGRES_DB"
```

**To restore into a fresh database:**

```bash
# Create the database first
PGPASSWORD="$POSTGRES_PASSWORD" createdb \
  -h localhost -p 5432 -U "$POSTGRES_USER" "$POSTGRES_DB"

# Then restore
gunzip -c ./backups/db/fax_20260317_030000.sql.gz | \
  PGPASSWORD="$POSTGRES_PASSWORD" psql \
    -h localhost -p 5432 \
    -U "$POSTGRES_USER" \
    -d "$POSTGRES_DB"
```

**If using Docker Compose:**

```bash
# Restore directly into the container
gunzip -c ./backups/db/fax_20260317_030000.sql.gz | \
  docker exec -i postgres psql -U fax -d fax
```

### 5b. Restore .env

```bash
# List available snapshots
ls -lh ./backups/env/

# Restore (simply copy back)
cp ./backups/env/.env_20260317_030000 ./.env
chmod 600 ./.env
```

### 5c. Restore FreeSWITCH Configuration

```bash
# List available archives
ls -lh ./backups/freeswitch/

# Preview contents before restoring
tar tzf ./backups/freeswitch/freeswitch_20260317_030000.tar.gz

# Restore on Path B (overwrites /etc/freeswitch)
sudo tar xzf ./backups/freeswitch/freeswitch_20260317_030000.tar.gz -C /etc/

# Restart FreeSWITCH to pick up changes
sudo systemctl restart freeswitch
```

On Path A, extract into the project's `volumes/` directory instead. The archive
contains both `freeswitch/` and, when present, the separate `gateways/` tree:

```bash
tar xzf ./backups/freeswitch/freeswitch_20260317_030000.tar.gz -C ./volumes/
```

### 5d. Restore GoFaxServer Configuration

```bash
# List available snapshots
ls -lh ./backups/gofaxserver-config/

# Restore on Path B (overwrites /etc/gofaxserver/config.json)
sudo cp ./backups/gofaxserver-config/config_20260317_030000.json /etc/gofaxserver/config.json
sudo chmod 600 /etc/gofaxserver/config.json

# Or restore on Path A (project-root config mounted into the container)
cp ./backups/gofaxserver-config/config_20260317_030000.json ./config.json
chmod 600 ./config.json
```

---

## 6. Downloading Backups from FTP

If you need to pull backups from the FTP server to restore on a new machine:

```bash
# Download all backups
wget -r -nH --cut-dirs=1 ftp://backupuser:backuppassword@ftp.example.com/backups/

# Or a specific file
curl -u backupuser:backuppassword \
  ftp://ftp.example.com/backups/db/fax_20260317_030000.sql.gz \
  -o fax_20260317_030000.sql.gz
```

---

## 7. Fax Portal Runtime State

`backup-all.sh` automatically includes the portal when its runtime config is
present. Portal backups are stored separately from the gofaxserver backups:

- `backups/portal-db/` — the complete `gofaxportal` PostgreSQL dump.
- `backups/portal-env/` — `portal/.env`, including session/encryption secrets.
- `backups/portal-config/` — the optional `portal/config.json`.

The portal database includes users, organizations, number assignments, job
history, audit logs, branding text, logo/favicon images, and encrypted received
fax PDFs. No separate branding-assets or fax-files backup is needed.

### Restore the portal

Restore the environment first so the database credentials and encryption key
are available. These examples restore into an empty portal database; do not run
them against a live database you intend to keep.

```bash
# Restore portal secrets/config
cp ./backups/portal-env/.env_TIMESTAMP ./portal/.env
chmod 600 ./portal/.env

# Only if your installation uses portal/config.json
cp ./backups/portal-config/config_TIMESTAMP.json ./portal/config.json
chmod 600 ./portal/config.json

# Load the restored database settings
set -a; source ./portal/.env; set +a

# Restore the portal database
gunzip -c ./backups/portal-db/gofaxportal_TIMESTAMP.sql.gz | \
  PGPASSWORD="$PORTAL_DB_PASSWORD" psql \
    -h "${PORTAL_DB_HOST:-127.0.0.1}" -p "${PORTAL_DB_PORT:-5433}" \
    -U "${PORTAL_DB_USER:-gofaxportal}" -d "${PORTAL_DB_NAME:-gofaxportal}"
```

> **Critical:** the portal DB stores per-organization gofaxserver
> service-account passwords **and received fax PDFs** sealed with AES-256-GCM
> using keys derived from the portal's `encryption_key`
> (`PORTAL_ENCRYPTION_KEY`). A restored database without the matching key makes
> those credentials and stored faxes permanently undecryptable. Always restore
> the matching `portal/.env` (and `portal/config.json`, if used) together with
> the database. Schema auto-migrates on portal start, so restoring into an empty
> database is supported.

---

## 8. Quick Reference

| Task | Command |
|---|---|
| Full backup | `sudo ./scripts/backup-all.sh` |
| GoFaxServer DB only | `./scripts/backup-db.sh` |
| Portal DB only | `./scripts/backup-portal-db.sh` |
| Restore GoFaxServer DB | `gunzip -c backup.sql.gz \| psql -U fax -d fax` |
| Restore GoFaxServer DB (Docker) | `gunzip -c backup.sql.gz \| docker exec -i postgres psql -U fax -d fax` |
| Restore GoFaxServer `.env` | `cp ./backups/env/.env_TIMESTAMP ./.env` |
| Restore portal `.env` | `cp ./backups/portal-env/.env_TIMESTAMP ./portal/.env` |
| Restore FreeSWITCH (Path B) | `sudo tar xzf backup.tar.gz -C /etc/` |
| Restore FreeSWITCH (Path A) | `tar xzf backup.tar.gz -C ./volumes/` |
| Restore GoFaxServer config | See section 5d for Path A/B destinations |
| Restore portal DB/config | See section 7 |
| Check backups | `ls -lhR ./backups/` |
