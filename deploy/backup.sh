#!/bin/sh
# Daily cron example: 0 2 * * * /opt/school/deploy/backup.sh
# Copy the output OFF the server too (object storage, another VPS, rclone...).
set -eu
cd "$(dirname "$0")/.."
mkdir -p backups
docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip > "backups/school-$(date +%F).sql.gz"
docker run --rm -v school-academic_uploads:/data -v "$PWD/backups":/out alpine \
  tar czf "/out/uploads-$(date +%F).tar.gz" -C /data .
find backups -type f -mtime +14 -delete
