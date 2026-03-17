#!/usr/bin/env bash
#
# backup-freeswitch.sh — Archive /etc/freeswitch to a timestamped tarball.
#
# Usage:  ./scripts/backup-freeswitch.sh [backup_dir]
#
# Defaults to ./backups/freeswitch/
# Must be run as root (or with sudo) to read /etc/freeswitch.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

FS_CONFIG_DIR="/etc/freeswitch"

if [[ ! -d "$FS_CONFIG_DIR" ]]; then
  echo "ERROR: FreeSWITCH config directory not found at $FS_CONFIG_DIR" >&2
  exit 1
fi

# ── Defaults ────────────────────────────────────────────────
BACKUP_DIR="${1:-$PROJECT_ROOT/backups/freeswitch}"
TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/freeswitch_${TIMESTAMP}.tar.gz"

# ── Archive ─────────────────────────────────────────────────
echo "[$(date)] Archiving $FS_CONFIG_DIR ..."

tar czf "$BACKUP_FILE" -C /etc freeswitch

SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
echo "[$(date)] Backup complete: $BACKUP_FILE ($SIZE)"

# ── Prune old backups ──────────────────────────────────────
PRUNED=$(find "$BACKUP_DIR" -name "freeswitch_*.tar.gz" -mtime +"$KEEP_DAYS" -print -delete | wc -l)
if [[ "$PRUNED" -gt 0 ]]; then
  echo "[$(date)] Pruned $PRUNED backup(s) older than ${KEEP_DAYS} days."
fi

echo "[$(date)] Done."
