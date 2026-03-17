#!/usr/bin/env bash
#
# backup-db.sh — Dump the PostgreSQL database to a timestamped, gzipped file.
#
# Usage:  ./scripts/backup-db.sh [backup_dir]
#
# The script reads credentials from .env in the project root.
# If no backup_dir is given, it defaults to ./backups/db/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"

# ── Load .env ───────────────────────────────────────────────
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: .env not found at $ENV_FILE" >&2
  exit 1
fi
set -a; source "$ENV_FILE"; set +a

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/db}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
DB_NAME="${POSTGRES_DB:-fax}"
DB_USER="${POSTGRES_USER:-fax}"
DB_HOST="${POSTGRES_HOST:-localhost}"
DB_PORT="${POSTGRES_PORT:-5432}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/${DB_NAME}_${TIMESTAMP}.sql.gz"

# ── Dump ────────────────────────────────────────────────────
echo "[$(date)] Starting PostgreSQL backup of '${DB_NAME}'..."

PGPASSWORD="$POSTGRES_PASSWORD" pg_dump \
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
