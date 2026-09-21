#!/usr/bin/env bash
# Build a GitHub Release-shaped Shipyard binary and optionally scan it.
# Quality CI and the Release workflow must both call this script so the
# linux/amd64 vulnerability gate cannot silently diverge from publish.
#
# Shape matches release linux/amd64:
#   - embedded frontend is built first
#   - GOOS=linux GOARCH=amd64 unless the caller already set GOOS/GOARCH
#   - TASKBOARD_LDFLAGS_EXTRA defaults to "-s -w"
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=go-toolchain.sh
source "$script_dir/go-toolchain.sh"

repo_root="$(shipyard_repo_root)"
cd "$repo_root"

export GOOS="${GOOS:-linux}"
export GOARCH="${GOARCH:-amd64}"
export TASKBOARD_LDFLAGS_EXTRA="${TASKBOARD_LDFLAGS_EXTRA:--s -w}"

if [[ "${TASKBOARD_SKIP_FRONTEND:-0}" != "1" ]]; then
  if [[ ! -f frontend/package.json ]]; then
    printf 'frontend/package.json is missing; cannot build a release-shaped artifact\n' >&2
    exit 1
  fi
  (
    cd frontend
    timeout "${TASKBOARD_FRONTEND_INSTALL_TIMEOUT:-120}s" npm ci
    timeout "${TASKBOARD_FRONTEND_BUILD_TIMEOUT:-120}s" npm run build
  )
fi

exec "$script_dir/build-taskboard.sh" "$@"
