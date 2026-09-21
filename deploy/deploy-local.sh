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
update_env_file="${TASKBOARD_ENV_FILE:-/etc/taskboard/taskboard.env}"

cleanup() { rm -f "$staged_binary"; }
trap cleanup EXIT

cd "$project_dir"
# shellcheck source=../scripts/go-toolchain.sh
source "$project_dir/scripts/go-toolchain.sh"
shipyard_export_build_toolchain
shipyard_require_patched_go
build_version="${TASKBOARD_VERSION:-$(git describe --tags --exact-match HEAD 2>/dev/null || printf 'development')}"
build_commit="${TASKBOARD_COMMIT_SHA:-$(git rev-parse HEAD)}"
build_time="${TASKBOARD_BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
build_go_version="$(shipyard_selected_go_version)"
export TASKBOARD_VERSION="$build_version"
export TASKBOARD_COMMIT_SHA="$build_commit"
export TASKBOARD_BUILD_TIME="$build_time"
export TASKBOARD_GO_VERSION="$build_go_version"
export TASKBOARD_EXPECTED_VERSION="$build_version"
export TASKBOARD_EXPECTED_COMMIT="$build_commit"
export TASKBOARD_EXPECTED_GO_VERSION="$build_go_version"
if [[ "${TASKBOARD_SKIP_UPDATE_CONFIG_CHECK:-0}" != "1" ]]; then
  if [[ ! -e "$update_env_file" ]]; then
    sudo -n install -o agent -g agent -m 600 deploy/taskboard.env.example "$update_env_file"
    echo "Update-Konfigurationsdatei wurde als Vorlage angelegt: $update_env_file" >&2
  fi
  TASKBOARD_ENV_FILE="$update_env_file" ./deploy/validate-update-config.sh
fi
if [[ -f frontend/package.json ]]; then
  (cd frontend && timeout 120s npm ci && timeout 120s npm run build)
fi
# Keep the Updates view tied to the exact source that was deployed. Operators
# may override these values for a development build, while tagged checkouts
# automatically expose their semantic release version and immutable commit.
./scripts/build-taskboard.sh -o "$staged_binary" --scan

# Migrations run at service start and are intentionally forward-only.  A
# complete logical backup before touching the live process is therefore the
# recovery point for both binary and schema changes.  Do not deploy when this
# safety net cannot be created and published successfully.
DATABASE_URL="$database_url" TASKBOARD_BACKUP_DIR="$backup_dir" ./deploy/backup-postgres.sh

sudo -n systemctl stop "$service_name"
sudo -n systemctl daemon-reload
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
