#!/usr/bin/env bash
# Build and roll out Taskboard without replacing a currently executing binary.
# If startup or the health check fails, the previous binary is restored.
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
service_name="taskboard.service"
staged_binary="$(mktemp /tmp/taskboard-build.XXXXXX)"
previous_binary="$project_dir/taskboard.previous"
live_binary="$project_dir/taskboard"
database_url="${DATABASE_URL:-postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable}"
backup_dir="${TASKBOARD_BACKUP_DIR:-/home/agent/taskboard-backups}"

cleanup() { rm -f "$staged_binary"; }
trap cleanup EXIT

cd "$project_dir"
if [[ -f frontend/package.json ]]; then
  (cd frontend && npm run build)
fi
go build -o "$staged_binary" ./cmd/taskboard
chmod 0755 "$staged_binary"

# Migrations run at service start and are intentionally forward-only.  A
# complete logical backup before touching the live process is therefore the
# recovery point for both binary and schema changes.  Do not deploy when this
# safety net cannot be created and published successfully.
DATABASE_URL="$database_url" TASKBOARD_BACKUP_DIR="$backup_dir" ./deploy/backup-postgres.sh

sudo -n systemctl stop "$service_name"
if [[ -f "$live_binary" ]]; then
  cp "$live_binary" "$previous_binary"
fi
mv "$staged_binary" "$live_binary"

rollback() {
  sudo -n systemctl stop "$service_name" || true
  if [[ -f "$previous_binary" ]]; then
    cp "$previous_binary" "$live_binary"
    sudo -n systemctl start "$service_name" || true
  fi
}

if ! sudo -n systemctl start "$service_name"; then
  rollback
  echo "Deployment fehlgeschlagen: Dienst konnte nicht gestartet werden." >&2
  exit 1
fi

for _ in {1..15}; do
  if systemctl is-active --quiet "$service_name" && curl -ksSf https://codex.local/healthz >/dev/null 2>&1; then
		# The first backup is the rollback point before forward-only migrations.
		# Publish a second snapshot after a healthy start so the release verifier
		# also has a restore point for the schema generation now in production.
		if ! DATABASE_URL="$database_url" TASKBOARD_BACKUP_DIR="$backup_dir" ./deploy/backup-postgres.sh; then
			break
		fi
    if ./deploy/verify-production.sh; then
      echo "Deployment erfolgreich."
      exit 0
    fi
    break
  fi
  sleep 1
done

rollback
echo "Deployment zurückgerollt: Gesundheitsprüfung nicht erfolgreich." >&2
exit 1
