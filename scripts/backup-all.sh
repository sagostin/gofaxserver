#!/usr/bin/env bash
#
# backup-all.sh — Run all backup scripts in sequence, optionally upload via FTP.
#
# Usage:  sudo ./scripts/backup-all.sh [backup_root]
#
# Defaults to ./backups/  (with sub-dirs db/, env/, freeswitch/)
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

echo "── [1/3] PostgreSQL database ──"
if "$SCRIPT_DIR/backup-db.sh" "$BACKUP_ROOT/db"; then
  echo ""
else
  echo "WARNING: Database backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [2/3] Environment file (.env) ──"
if "$SCRIPT_DIR/backup-env.sh" "$BACKUP_ROOT/env"; then
  echo ""
else
  echo "WARNING: .env backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

echo "── [3/3] FreeSWITCH configs ──"
if "$SCRIPT_DIR/backup-freeswitch.sh" "$BACKUP_ROOT/freeswitch"; then
  echo ""
else
  echo "WARNING: FreeSWITCH backup failed!" >&2
  FAILED=$((FAILED + 1))
  echo ""
fi

# ── FTP Upload (optional) ──────────────────────────────────
echo "── [FTP] Uploading backups ──"
for SUBDIR in db env freeswitch; do
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

