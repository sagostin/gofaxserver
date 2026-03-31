#!/usr/bin/env bash
#
# backup-gofaxserver-config.sh — Snapshot /etc/gofaxserver/config.json.
#
# Usage:  sudo ./scripts/backup-gofaxserver-config.sh [backup_dir]
#
# Defaults to ./backups/gofaxserver-config/
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GOFXSERVER_CONFIG="/etc/gofaxserver/config.json"

if [[ ! -f "$GOFXSERVER_CONFIG" ]]; then
  echo "ERROR: gofaxserver config not found at $GOFXSERVER_CONFIG" >&2
  exit 1
fi

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/gofaxserver-config}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/config_${TIMESTAMP}.json"

# ── Copy ────────────────────────────────────────────────────
cp "$GOFXSERVER_CONFIG" "$BACKUP_FILE"
chmod 600 "$BACKUP_FILE"

echo "[$(date)] gofaxserver config backed up to $BACKUP_FILE"

# ── Prune old backups ──────────────────────────────────────
PRUNED=$(find "$BACKUP_DIR" -name "config_*.json" -mtime +"$KEEP_DAYS" -print -delete | wc -l)
if [[ "$PRUNED" -gt 0 ]]; then
  echo "[$(date)] Pruned $PRUNED backup(s) older than ${KEEP_DAYS} days."
fi

echo "[$(date)] Done."
