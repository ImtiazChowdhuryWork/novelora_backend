#!/usr/bin/env bash
# Backs up the Postgres database and the uploads/ directory to
# timestamped, gzip-compressed files, then prunes backups older than
# RETENTION_DAYS. Reads DATABASE_URL/UPLOADS_DIR the same way the server
# itself does (see internal/config/config.go), sourcing .env if present.
#
# Usage: ./scripts/backup.sh
# Cron (daily at 03:00): 0 3 * * * cd /path/to/novelora_backend && ./scripts/backup.sh >> backup.log 2>&1
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-./backups}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

if [ -z "${DATABASE_URL:-}" ]; then
  echo "DATABASE_URL is not set (checked environment and .env)" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"

echo "Dumping database..."
pg_dump "$DATABASE_URL" | gzip > "$BACKUP_DIR/db_${TIMESTAMP}.sql.gz"

UPLOADS_DIR="${UPLOADS_DIR:-uploads}"
if [ -d "$UPLOADS_DIR" ]; then
  echo "Archiving uploads..."
  tar -czf "$BACKUP_DIR/uploads_${TIMESTAMP}.tar.gz" "$UPLOADS_DIR"
else
  echo "Uploads directory '$UPLOADS_DIR' not found, skipping"
fi

echo "Pruning backups older than ${RETENTION_DAYS} days..."
find "$BACKUP_DIR" -type f -mtime "+${RETENTION_DAYS}" -delete

echo "Backup complete: $BACKUP_DIR/db_${TIMESTAMP}.sql.gz, $BACKUP_DIR/uploads_${TIMESTAMP}.tar.gz"
