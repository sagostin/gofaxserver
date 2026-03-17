#!/usr/bin/env bash
#
# backup-env.sh — Snapshot the .env file to a timestamped copy.
#
# Usage:  ./scripts/backup-env.sh [backup_dir]
#
# Defaults to ./backups/env/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: .env not found at $ENV_FILE" >&2
  exit 1
fi

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/env}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-90}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/.env_${TIMESTAMP}"

# ── Copy ────────────────────────────────────────────────────
cp "$ENV_FILE" "$BACKUP_FILE"
chmod 600 "$BACKUP_FILE"

echo "[$(date)] .env backed up to $BACKUP_FILE"

# ── Prune old backups ──────────────────────────────────────
PRUNED=$(find "$BACKUP_DIR" -name ".env_*" -mtime +"$KEEP_DAYS" -print -delete | wc -l)
if [[ "$PRUNED" -gt 0 ]]; then
  echo "[$(date)] Pruned $PRUNED backup(s) older than ${KEEP_DAYS} days."
fi

echo "[$(date)] Done."
