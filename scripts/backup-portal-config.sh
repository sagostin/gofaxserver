#!/usr/bin/env bash
#
# backup-portal-config.sh — Snapshot the optional Fax Portal config.json.
#
# Usage:  ./scripts/backup-portal-config.sh [backup_dir]
#
# Defaults to ./backups/portal-config/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PORTAL_DIR="${PORTAL_DIR:-$PROJECT_ROOT/portal}"
CONFIG_FILE="${PORTAL_CONFIG:-$PORTAL_DIR/config.json}"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "ERROR: Fax Portal config not found at $CONFIG_FILE" >&2
  exit 1
fi

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/portal-config}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/config_${TIMESTAMP}.json"

# ── Copy ────────────────────────────────────────────────────
cp "$CONFIG_FILE" "$BACKUP_FILE"
chmod 600 "$BACKUP_FILE"

echo "[$(date)] Fax Portal config backed up to $BACKUP_FILE"

# ── Prune old backups ──────────────────────────────────────
PRUNED=$(find "$BACKUP_DIR" -name "config_*.json" -mtime +"$KEEP_DAYS" -print -delete | wc -l)
if [[ "$PRUNED" -gt 0 ]]; then
  echo "[$(date)] Pruned $PRUNED backup(s) older than ${KEEP_DAYS} days."
fi

echo "[$(date)] Done."
