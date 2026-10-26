#!/usr/bin/env bash
#
# backup-all.sh — Run all backup scripts in sequence, optionally upload via FTP.
#
# Usage:  sudo ./scripts/backup-all.sh [backup_root]
#
# Defaults to ./backups/ (db/, env/, freeswitch/, gofaxserver-config/,
# portal-db/, portal-env/, and portal-config/).
# Portal steps run when portal/.env or portal/config.json exists.
# Set FTP_HOST, FTP_USER, FTP_PASS in .env to enable FTP upload.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BACKUP_ROOT="${1:-$PROJECT_ROOT/backups}"

echo "============================================"
echo "  GoFaxServer — Full Backup"
echo "  $(date)"
echo "============================================"
echo ""

FAILED=0

echo "── [1/7] PostgreSQL database ──"
if "$SCRIPT_DIR/backup-db.sh" "$BACKUP_ROOT/db"; then
  echo ""
else
  echo "WARNING: Database backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [2/7] Environment file (.env) ──"
if "$SCRIPT_DIR/backup-env.sh" "$BACKUP_ROOT/env"; then
  echo ""
else
  echo "WARNING: .env backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [3/7] FreeSWITCH configs ──"
if "$SCRIPT_DIR/backup-freeswitch.sh" "$BACKUP_ROOT/freeswitch"; then
  echo ""
else
  echo "WARNING: FreeSWITCH backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [4/7] GoFaxServer config ──"
if "$SCRIPT_DIR/backup-gofaxserver-config.sh" "$BACKUP_ROOT/gofaxserver-config"; then
  echo ""
else
  echo "WARNING: GoFaxServer config backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

PORTAL_DIR="${PORTAL_DIR:-$PROJECT_ROOT/portal}"
PORTAL_ENV_FILE="${PORTAL_ENV_FILE:-$PORTAL_DIR/.env}"
PORTAL_CONFIG="${PORTAL_CONFIG:-$PORTAL_DIR/config.json}"

echo "── [5/7] Fax Portal database ──"
if [[ ! -f "$PORTAL_ENV_FILE" && ! -f "$PORTAL_CONFIG" ]]; then
  echo "SKIP: Fax Portal runtime config not found; database backup skipped."
  echo ""
elif "$SCRIPT_DIR/backup-portal-db.sh" "$BACKUP_ROOT/portal-db"; then
  echo ""
else
  echo "WARNING: Fax Portal database backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [6/7] Fax Portal environment (.env) ──"
if [[ ! -f "$PORTAL_ENV_FILE" ]]; then
  echo "SKIP: Fax Portal .env not found at $PORTAL_ENV_FILE"
  echo ""
elif "$SCRIPT_DIR/backup-portal-env.sh" "$BACKUP_ROOT/portal-env"; then
  echo ""
else
  echo "WARNING: Fax Portal .env backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [7/7] Fax Portal config ──"
if [[ ! -f "$PORTAL_CONFIG" ]]; then
  echo "SKIP: Optional Fax Portal config not found at $PORTAL_CONFIG"
  echo ""
elif "$SCRIPT_DIR/backup-portal-config.sh" "$BACKUP_ROOT/portal-config"; then
  echo ""
else
  echo "WARNING: Fax Portal config backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

# ── FTP Upload (optional) ──────────────────────────────────
echo "── [FTP] Uploading backups ──"
for SUBDIR in db env freeswitch gofaxserver-config portal-db portal-env portal-config; do
  DIR="$BACKUP_ROOT/$SUBDIR"
  [[ -d "$DIR" ]] || continue
  if "$SCRIPT_DIR/backup-ftp-upload.sh" "$DIR" "$SUBDIR"; then
    :
  else
    echo "WARNING: FTP upload of $SUBDIR failed!" >&2
    FAILED=$((FAILED + 1))
  fi
done
echo ""

echo "============================================"
if [[ "$FAILED" -gt 0 ]]; then
  echo "  Completed with $FAILED failure(s)."
  exit 1
else
  echo "  All backups completed successfully."
fi
echo "============================================"

