#!/usr/bin/env bash
#
# backup-ftp-upload.sh — Upload a backup directory's contents to an FTP server.
#
# Usage:  ./scripts/backup-ftp-upload.sh <local_backup_dir> [remote_subdir]
#
# FTP settings are read from .env (or environment):
#   FTP_HOST, FTP_USER, FTP_PASS, FTP_PORT (default 21), FTP_REMOTE_DIR (default /backups)
#
# Requires: curl (ships with most distros)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"

# ── Load .env (if present) ──────────────────────────────────
if [[ -f "$ENV_FILE" ]]; then
  set -a; source "$ENV_FILE"; set +a
fi

# ── Validate FTP config ────────────────────────────────────
if [[ -z "${FTP_HOST:-}" || -z "${FTP_USER:-}" || -z "${FTP_PASS:-}" ]]; then
  echo "SKIP: FTP upload not configured (set FTP_HOST, FTP_USER, FTP_PASS in .env)" >&2
  exit 0
fi

LOCAL_DIR="${1:?Usage: backup-ftp-upload.sh <local_backup_dir> [remote_subdir]}"
REMOTE_SUBDIR="${2:-}"

FTP_PORT="${FTP_PORT:-21}"
FTP_REMOTE_DIR="${FTP_REMOTE_DIR:-/backups}"

# Build the full remote path
if [[ -n "$REMOTE_SUBDIR" ]]; then
  REMOTE_PATH="${FTP_REMOTE_DIR%/}/${REMOTE_SUBDIR}"
else
  REMOTE_PATH="${FTP_REMOTE_DIR}"
fi

if [[ ! -d "$LOCAL_DIR" ]]; then
  echo "ERROR: Local directory not found: $LOCAL_DIR" >&2
  exit 1
fi

# ── Create remote directory (curl --ftp-create-dirs) ───────
echo "[$(date)] Uploading backups to ftp://${FTP_HOST}:${FTP_PORT}${REMOTE_PATH}/ ..."

UPLOADED=0
FAILED=0

for FILE in "$LOCAL_DIR"/*; do
  [[ -f "$FILE" ]] || continue

  BASENAME="$(basename "$FILE")"
  REMOTE_URL="ftp://${FTP_HOST}:${FTP_PORT}${REMOTE_PATH}/${BASENAME}"

  if curl --silent --show-error \
       --ftp-create-dirs \
       --user "${FTP_USER}:${FTP_PASS}" \
       --upload-file "$FILE" \
       "$REMOTE_URL"; then
    echo "  ✓ $BASENAME"
    UPLOADED=$((UPLOADED + 1))
  else
    echo "  ✗ $BASENAME (upload failed)" >&2
    FAILED=$((FAILED + 1))
  fi
done

echo "[$(date)] FTP upload complete: $UPLOADED uploaded, $FAILED failed."

if [[ "$FAILED" -gt 0 ]]; then
  exit 1
fi
