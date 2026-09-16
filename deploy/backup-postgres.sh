#!/usr/bin/env bash
set -euo pipefail

# Creates a compressed, timestamped logical backup. Run from a systemd timer,
# cron, or the host's backup service. DATABASE_URL must point at Taskboard.
: "${DATABASE_URL:?DATABASE_URL is required}"
backup_dir="${TASKBOARD_BACKUP_DIR:-/var/backups/taskboard}"
mkdir -p "$backup_dir"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_path="$backup_dir/taskboard-$stamp.dump"
metadata_path="${backup_path%.dump}.meta"
temporary_dir="$(mktemp -d "$backup_dir/.taskboard-backup.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT

# A readable dump is not enough: it must describe the same schema generation
# as the database. Capture before and after the dump so a concurrent migration
# cannot be silently stamped onto an older backup.
before_migrations="$(psql "$DATABASE_URL" --tuples-only --no-align --command 'SELECT count(*) FROM schema_migrations')"
pg_dump --dbname="$DATABASE_URL" --format=custom --file="$temporary_dir/taskboard.dump"
after_migrations="$(psql "$DATABASE_URL" --tuples-only --no-align --command 'SELECT count(*) FROM schema_migrations')"
if [[ "$before_migrations" != "$after_migrations" ]]; then
  printf 'schema changed while creating backup (%s -> %s); retry the backup\n' "$before_migrations" "$after_migrations" >&2
  exit 1
fi

# Store a content digest beside the schema marker. pg_restore --list proves
# that an archive can be opened; the digest additionally detects bit rot or a
# partial copy after the atomic local publish and before a restore drill.
checksum="$(sha256sum "$temporary_dir/taskboard.dump" | awk '{print $1}')"
printf 'schema_migrations=%s\ncreated_at=%s\nsha256=%s\n' "$after_migrations" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$checksum" > "$temporary_dir/taskboard.meta"
# Publish dump and schema marker only after both have been written completely.
mv "$temporary_dir/taskboard.dump" "$backup_path"
mv "$temporary_dir/taskboard.meta" "$metadata_path"
find "$backup_dir" -type f \( -name 'taskboard-*.dump' -o -name 'taskboard-*.meta' \) -mtime +14 -delete
