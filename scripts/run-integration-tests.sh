#!/usr/bin/env bash
set -Eeuo pipefail

# Runs the opt-in PostgreSQL and listener integration suite on a host/CI runner
# with network namespaces enabled. The database is disposable and is never
# taken from DATABASE_URL.

if ! command -v podman >/dev/null 2>&1; then
  echo "podman is required to run the isolated integration suite" >&2
  exit 1
fi

repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
run_dir=$(mktemp -d "${TMPDIR:-/tmp}/shipyard-integration.XXXXXX")
container_name="shipyard-test-postgres-${RANDOM}-${RANDOM}"
container_started=0

cleanup() {
  if [[ "$container_started" == 1 ]]; then
    podman rm --force "$container_name" >/dev/null 2>&1 || true
  fi
  rm -rf "$run_dir"
}
trap cleanup EXIT INT TERM

podman run --detach --rm \
  --name "$container_name" \
  --env POSTGRES_USER=taskboard \
  --env POSTGRES_PASSWORD=taskboard \
  --env POSTGRES_DB=taskboard_agent_tests \
  --publish 127.0.0.1::5432 \
  docker.io/library/postgres:16-alpine >/dev/null
container_started=1

for attempt in {1..60}; do
  if podman exec "$container_name" pg_isready \
      --username=taskboard --dbname=taskboard_agent_tests >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == 60 ]]; then
    echo "temporary PostgreSQL did not become ready" >&2
    exit 1
  fi
  sleep 1
done

published_port=$(podman port "$container_name" 5432/tcp | sed -n '1s/.*://p')
if [[ -z "$published_port" ]]; then
  echo "could not determine the temporary PostgreSQL listener" >&2
  exit 1
fi

cd "$repo_dir"
SHIPYARD_TEST_DATABASE_URL="postgres://taskboard:taskboard@127.0.0.1:${published_port}/taskboard_agent_tests?sslmode=disable" \
  GOCACHE="$run_dir/go-cache" \
  go test ./...
