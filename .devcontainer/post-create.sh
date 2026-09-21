#!/usr/bin/env bash
# Prepare a Dev Container checkout: module download, frontend install, and the
# isolated test database. Host shells can run the same script; database setup
# stays behind SHIPYARD_DEVCONTAINER so a host DATABASE_URL is left untouched.
set -Eeuo pipefail

repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

if [[ "${SHIPYARD_DEVCONTAINER:-}" == "1" ]]; then
  if ! git config --global --get-all safe.directory 2>/dev/null | grep -Fxq "$repo_dir"; then
    git config --global --add safe.directory "$repo_dir"
  fi

  modules="${repo_dir}/frontend/node_modules"
  sudo mkdir -p "$modules"
  sudo chown "$(id -u):$(id -g)" "$modules"

  if [[ -z "${DATABASE_URL:-}" ]]; then
    printf 'DATABASE_URL is not set; the Dev Container database was not initialized.\n' >&2
    exit 1
  fi
  for attempt in $(seq 1 60); do
    if psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -tAc 'SELECT 1' >/dev/null 2>&1; then
      break
    fi
    if [[ "$attempt" == 60 ]]; then
      printf 'PostgreSQL did not become ready.\n' >&2
      exit 1
    fi
    sleep 1
  done
  exists="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -tAc "SELECT 1 FROM pg_database WHERE datname = 'taskboard_agent_tests'" | tr -d '[:space:]')"
  if [[ "$exists" != "1" ]]; then
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c 'CREATE DATABASE taskboard_agent_tests OWNER taskboard' >/dev/null
  fi
fi

install_frontend() {
  # npm ci deletes node_modules before installing. A Compose volume cannot be
  # removed, so the locked install runs in a staging directory and is copied
  # onto the volume. Host checkouts keep the normal npm ci path.
  if [[ "${SHIPYARD_DEVCONTAINER:-}" == "1" ]] && mountpoint -q frontend/node_modules; then
    local staging
    staging="$(mktemp -d)"
    cp frontend/package.json frontend/package-lock.json "$staging/"
    npm ci --prefix "$staging"
    find frontend/node_modules -mindepth 1 -delete
    cp -a "$staging/node_modules/." frontend/node_modules/
    rm -rf "$staging"
    return
  fi
  npm ci --prefix frontend
}

go mod download
install_frontend

if [[ "${SHIPYARD_DEVCONTAINER:-}" == "1" && ! -x "${CHROMIUM_PATH:-/usr/bin/chromium}" ]]; then
  printf 'Chromium is not executable at %s.\n' "${CHROMIUM_PATH:-/usr/bin/chromium}" >&2
  exit 1
fi

printf 'Shipyard dependencies are installed.\n'
