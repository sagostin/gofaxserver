#!/usr/bin/env bash
#
# backup-portal-db.sh — Dump the Fax Portal PostgreSQL database.
#
# Usage:  ./scripts/backup-portal-db.sh [backup_dir]
#
# Reads PORTAL_DB_* from portal/.env when present, or from the environment.
# Defaults to ./backups/portal-db/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PORTAL_DIR="${PORTAL_DIR:-$PROJECT_ROOT/portal}"
ENV_FILE="${PORTAL_ENV_FILE:-$PORTAL_DIR/.env}"

# ── Load portal .env (if present) ────────────────────────────
if [[ -f "$ENV_FILE" ]]; then
  set -a; source "$ENV_FILE"; set +a
fi

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/portal-db}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
DB_NAME="${PORTAL_DB_NAME:-gofaxportal}"
DB_USER="${PORTAL_DB_USER:-gofaxportal}"
DB_HOST="${PORTAL_DB_HOST:-127.0.0.1}"
DB_PORT="${PORTAL_DB_PORT:-5433}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"

if [[ -z "${PORTAL_DB_PASSWORD:-}" ]]; then
  echo "ERROR: PORTAL_DB_PASSWORD is not set (checked $ENV_FILE and the environment)" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/${DB_NAME}_${TIMESTAMP}.sql.gz"

# ── Dump ────────────────────────────────────────────────────
echo "[$(date)] Starting Fax Portal PostgreSQL backup of '${DB_NAME}'..."

PGPASSWORD="$PORTAL_DB_PASSWORD" pg_dump \
  -h "$DB_HOST" \
  -p "$DB_PORT" \
  -U "$DB_USER" \
  -d "$DB_NAME" \
  --no-owner \
  --no-privileges \
  --format=plain \
  | gzip > "$BACKUP_FILE"

SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
echo "[$(date)] Backup complete: $BACKUP_FILE ($SIZE)"

# ── Prune old backups ──────────────────────────────────────
PRUNED=$(find "$BACKUP_DIR" -name "*.sql.gz" -mtime +"$KEEP_DAYS" -print -delete | wc -l)
if [[ "$PRUNED" -gt 0 ]]; then
  echo "[$(date)] Pruned $PRUNED backup(s) older than ${KEEP_DAYS} days."
fi

echo "[$(date)] Done."
