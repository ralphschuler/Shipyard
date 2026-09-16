#!/usr/bin/env bash
set -euo pipefail

# Restore drill for a deliberately separate, empty PostgreSQL database.
# It never derives a target from DATABASE_URL and refuses both the live
# database and any database that already contains public tables.

: "${DATABASE_URL:?DATABASE_URL is required (the live source database)}"
: "${TASKBOARD_RESTORE_DATABASE:?TASKBOARD_RESTORE_DATABASE is required (an empty test database)}"

backup_dir="${TASKBOARD_BACKUP_DIR:-/home/agent/taskboard-backups}"
backup_path="${TASKBOARD_RESTORE_BACKUP:-}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'missing required command: %s\n' "$1" >&2
    exit 1
  }
}

for command in psql pg_restore sha256sum awk find sort tail; do
  require "$command"
done

if [[ -z "$backup_path" ]]; then
  backup_path="$(find "$backup_dir" -maxdepth 1 -type f -name 'taskboard-*.dump' -printf '%T@ %p\n' | sort -n | tail -1 | cut -d' ' -f2-)"
fi
if [[ -z "$backup_path" || ! -r "$backup_path" ]]; then
  printf 'no readable Taskboard backup found\n' >&2
  exit 1
fi

metadata_path="${backup_path%.dump}.meta"
if [[ ! -r "$metadata_path" ]]; then
  printf 'backup metadata missing for %s\n' "$(basename "$backup_path")" >&2
  exit 1
fi
expected_checksum="$(awk -F= '$1 == "sha256" {print $2}' "$metadata_path")"
actual_checksum="$(sha256sum "$backup_path" | awk '{print $1}')"
if [[ ! "$expected_checksum" =~ ^[[:xdigit:]]{64}$ || "$expected_checksum" != "$actual_checksum" ]]; then
  printf 'backup checksum verification failed for %s\n' "$(basename "$backup_path")" >&2
  exit 1
fi

source_database="$(psql "$DATABASE_URL" --tuples-only --no-align --command 'SELECT current_database()')"
target_database="$(psql "$TASKBOARD_RESTORE_DATABASE" --tuples-only --no-align --command 'SELECT current_database()')"
if [[ "$source_database" == "$target_database" ]]; then
  printf 'restore target must not be the live database (%s)\n' "$source_database" >&2
  exit 1
fi

existing_tables="$(psql "$TASKBOARD_RESTORE_DATABASE" --tuples-only --no-align --command "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'")"
if [[ "$existing_tables" != "0" ]]; then
  printf 'restore target %s is not empty (%s public tables); refusing to overwrite it\n' "$target_database" "$existing_tables" >&2
  exit 1
fi

# No --clean: the empty-target assertion above is the safety boundary.
pg_restore --exit-on-error --no-owner --no-privileges --dbname="$TASKBOARD_RESTORE_DATABASE" "$backup_path"

expected_migrations="$(awk -F= '$1 == "schema_migrations" {print $2}' "$metadata_path")"
restored_migrations="$(psql "$TASKBOARD_RESTORE_DATABASE" --tuples-only --no-align --command 'SELECT count(*) FROM schema_migrations')"
if [[ "$expected_migrations" != "$restored_migrations" ]]; then
  printf 'restore schema mismatch: backup=%s restored=%s\n' "$expected_migrations" "$restored_migrations" >&2
  exit 1
fi

tables_after_restore="$(psql "$TASKBOARD_RESTORE_DATABASE" --tuples-only --no-align --command "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'")"
if [[ "$tables_after_restore" == "0" ]]; then
  printf 'restore produced no application tables\n' >&2
  exit 1
fi

printf 'restore drill passed: backup=%s target=%s migrations=%s tables=%s\n' \
  "$(basename "$backup_path")" "$target_database" "$restored_migrations" "$tables_after_restore"
