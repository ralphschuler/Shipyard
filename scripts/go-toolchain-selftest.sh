#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=go-toolchain.sh
source "$script_dir/go-toolchain.sh"

failures=0
fail() {
  printf 'go-toolchain self-test: %s\n' "$1" >&2
  failures=$((failures + 1))
}

minimum="$(shipyard_minimum_go_toolchain)"
if [[ "$minimum" != go1.* ]]; then
  fail "go.mod toolchain was $minimum"
fi

shipyard_go_version_ge go1.26.8 "$minimum" || fail "declared minimum $minimum should be satisfied by go1.26.8 or itself"
if shipyard_go_version_ge go1.26.0 "$minimum"; then
  fail "go1.26.0 must not satisfy $minimum"
fi
shipyard_go_version_ge go1.27.0 "$minimum" || fail "go1.27.0 should satisfy $minimum"

normalized="$(shipyard_normalize_go_version 'go1.26.8 linux/amd64')"
if [[ "$normalized" != go1.26.8 ]]; then
  fail "normalize produced $normalized"
fi

if [[ "$minimum" == go1.26.8 ]]; then
  if shipyard_require_patched_go "$(shipyard_repo_root)/go.mod" go1.26.0 >/dev/null; then
    fail "require-go-toolchain accepted go1.26.0"
  fi
fi

if (( failures > 0 )); then
  exit 1
fi
printf 'go-toolchain self-test passed (minimum %s).\n' "$minimum"
