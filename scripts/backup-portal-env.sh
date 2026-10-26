#!/usr/bin/env bash
#
# backup-portal-env.sh — Snapshot the Fax Portal .env file.
#
# Usage:  ./scripts/backup-portal-env.sh [backup_dir]
#
# Defaults to ./backups/portal-env/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PORTAL_DIR="${PORTAL_DIR:-$PROJECT_ROOT/portal}"
ENV_FILE="${PORTAL_ENV_FILE:-$PORTAL_DIR/.env}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: Fax Portal .env not found at $ENV_FILE" >&2
  exit 1
fi

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/portal-env}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-90}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/.env_${TIMESTAMP}"

# ── Copy ────────────────────────────────────────────────────
cp "$ENV_FILE" "$BACKUP_FILE"
chmod 600 "$BACKUP_FILE"

echo "[$(date)] Fax Portal .env backed up to $BACKUP_FILE"

# ── Prune old backups ──────────────────────────────────────
PRUNED=$(find "$BACKUP_DIR" -name ".env_*" -mtime +"$KEEP_DAYS" -print -delete | wc -l)
if [[ "$PRUNED" -gt 0 ]]; then
  echo "[$(date)] Pruned $PRUNED backup(s) older than ${KEEP_DAYS} days."
fi

echo "[$(date)] Done."
