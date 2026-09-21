#!/usr/bin/env bash
# Build the embedded production binary and run the Playwright bundle smoke
# test against it. The listener stays on 127.0.0.1:18080 inside this
# container so it does not take the default application port.
set -Eeuo pipefail

repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

if [[ "${SHIPYARD_DEVCONTAINER:-}" == "1" ]]; then
  if ! git config --global --get-all safe.directory 2>/dev/null | grep -Fxq "$repo_dir"; then
    git config --global --add safe.directory "$repo_dir"
  fi
fi

addr="${SHIPYARD_EMBEDDED_E2E_ADDR:-127.0.0.1:18080}"
log="${TMPDIR:-/tmp}/shipyard-embedded-e2e.log"
binary="${TMPDIR:-/tmp}/taskboard-embedded"
pid=""

cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

npm run build --prefix frontend
./scripts/build-taskboard.sh -o "$binary"

TASKBOARD_ADDR="$addr" "$binary" --embedded-app-http >"$log" 2>&1 &
pid=$!

ready=0
for _ in $(seq 1 30); do
  if curl --fail --silent --max-time 2 "http://${addr}/app/" >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
if [[ "$ready" != 1 ]]; then
  printf 'Embedded server did not serve /app/.\n' >&2
  tail -n 40 "$log" >&2 || true
  exit 1
fi

TASKBOARD_E2E_BASE_URL="http://${addr}" npm run test:e2e --prefix frontend -- e2e/embedded-bundle.spec.ts
