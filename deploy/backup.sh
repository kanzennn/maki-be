#!/bin/sh
# Backs the database up to the backup bucket: a pg_dump in its custom format,
# which pg_restore reads, kept here for a week and in the bucket for as long
# as its lifecycle rule says (14–30 days, spec). Cron runs it daily from
# /srv/maki; docs/deployment.md has the cron line and the restore.
#
#   sh backup.sh
set -eu
cd "$(dirname "$0")"

if [ ! -f backup.env ]; then
  echo "backup.sh: backup.env is missing: copy backup.env.example and fill it in" >&2
  exit 1
fi
setting() { sed -n "s/^$1=//p" backup.env; }
endpoint="$(setting BACKUP_ENDPOINT)"
bucket="$(setting BACKUP_BUCKET)"

mkdir -p backups
file="maki-$(date -u +%Y%m%dT%H%M%SZ).dump"
# pg_dump from the stack's pg service (compose.yaml), on the shared server's
# network, with this app's DB_ variables, which the single quotes leave to
# the container's shell.
docker compose --env-file .env --env-file release.env run --rm -T pg sh -c \
  'PGPASSWORD="$DB_PASSWORD" PGSSLMODE="${DB_SSLMODE:-prefer}" exec pg_dump -w -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USERNAME" -d "$DB_DATABASE" --format=custom' \
  > "backups/$file.part"
mv "backups/$file.part" "backups/$file"

docker run --rm --env-file backup.env -v "$(pwd)/backups:/backups:ro" amazon/aws-cli \
  s3 cp "/backups/$file" "s3://$bucket/$file" --endpoint-url "$endpoint"

find backups -name 'maki-*.dump' -mtime +7 -delete
echo "backup.sh: $file is in s3://$bucket"
