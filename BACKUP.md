# GoFaxServer — Backup & Restore Guide

## Overview

The `scripts/` directory contains five backup scripts:

| Script | Purpose |
|---|---|
| `backup-db.sh` | Dump PostgreSQL database (gzipped) |
| `backup-env.sh` | Snapshot `.env` file |
| `backup-freeswitch.sh` | Archive `/etc/freeswitch` (tarball) |
| `backup-ftp-upload.sh` | Upload backup files to an FTP server |
| `backup-all.sh` | Runs all of the above in sequence |

All backups are saved to `./backups/<type>/` by default, with timestamped filenames.

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

> **Note:** If `FTP_HOST` is not set, the FTP upload step is silently skipped.

---

## 2. Running Backups Manually

### Full backup (all three + FTP upload)

```bash
sudo ./scripts/backup-all.sh
```

### Individual backups

```bash
./scripts/backup-db.sh                    # database only
./scripts/backup-env.sh                   # .env only
sudo ./scripts/backup-freeswitch.sh       # freeswitch only (needs root)
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
| Database (`.sql.gz`) | 30 days | `BACKUP_KEEP_DAYS` |
| .env snapshots | 90 days | `BACKUP_KEEP_DAYS` |
| FreeSWITCH (`.tar.gz`) | 30 days | `BACKUP_KEEP_DAYS` |

-- -

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

# Restore (overwrites current /etc/freeswitch)
sudo tar xzf ./backups/freeswitch/freeswitch_20260317_030000.tar.gz -C /etc/

# Restart FreeSWITCH to pick up changes
sudo systemctl restart freeswitch
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

## 7. Quick Reference

| Task | Command |
|---|---|
| Full backup | `sudo ./scripts/backup-all.sh` |
| DB only | `./scripts/backup-db.sh` |
| Restore DB | `gunzip -c backup.sql.gz \| psql -U fax -d fax` |
| Restore DB (Docker) | `gunzip -c backup.sql.gz \| docker exec -i postgres psql -U fax -d fax` |
| Restore .env | `cp ./backups/env/.env_TIMESTAMP ./.env` |
| Restore FreeSWITCH | `sudo tar xzf backup.tar.gz -C /etc/` |
| Check backups | `ls -lhR ./backups/` |
