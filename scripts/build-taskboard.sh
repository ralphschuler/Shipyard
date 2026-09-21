#!/usr/bin/env bash
# Build the Shipyard server with the patched Go toolchain declared in go.mod.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=go-toolchain.sh
source "$script_dir/go-toolchain.sh"

repo_root="$(shipyard_repo_root)"
output=""
scan=0
extra_ldflags="${TASKBOARD_LDFLAGS_EXTRA:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output)
      output="${2:-}"
      shift 2
      ;;
    --scan)
      scan=1
      shift
      ;;
    *)
      printf 'usage: %s -o <binary> [--scan]\n' "$0" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$output" ]]; then
  printf 'usage: %s -o <binary> [--scan]\n' "$0" >&2
  exit 2
fi

cd "$repo_root"
shipyard_export_build_toolchain
shipyard_require_patched_go

build_version="${TASKBOARD_VERSION:-$(git describe --tags --exact-match HEAD 2>/dev/null || printf 'development')}"
build_commit="${TASKBOARD_COMMIT_SHA:-$(git rev-parse HEAD)}"
build_time="${TASKBOARD_BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
go_version="$(shipyard_selected_go_version)"

metadata_ldflags="-X main.version=${build_version} -X main.commit=${build_commit} -X main.builtAt=${build_time} -X main.goversion=${go_version}"

# -s/-w strip the symbol table and DWARF govulncheck binary mode needs.
# Scanning a stripped binary reports unused packages from a present module
# (GO-2026-5932 openpgp next to argon2) as wildcard symbol hits. Drop those
# flags for the scanned compile; re-apply them on the shipped artifact.
scan_extra=""
for flag in $extra_ldflags; do
  case "$flag" in
    -s|-w) ;;
    *)
      if [[ -n "$scan_extra" ]]; then
        scan_extra+=" $flag"
      else
        scan_extra="$flag"
      fi
      ;;
  esac
done

scan_ldflags="$metadata_ldflags"
if [[ -n "$scan_extra" ]]; then
  scan_ldflags="${scan_extra} ${metadata_ldflags}"
fi
ship_ldflags="$metadata_ldflags"
if [[ -n "$extra_ldflags" ]]; then
  ship_ldflags="${extra_ldflags} ${metadata_ldflags}"
fi

build_binary() {
  local flags="$1"
  mkdir -p "$(dirname "$output")"
  timeout "${TASKBOARD_BUILD_TIMEOUT:-120}s" go build -trimpath -ldflags "$flags" -o "$output" ./cmd/taskboard
  chmod 0755 "$output"

  local artifact_version
  artifact_version="$(shipyard_artifact_go_version "$output")"
  if ! shipyard_go_version_ge "$artifact_version" "$GOTOOLCHAIN_MINIMUM"; then
    printf 'built artifact embeds %s, which is older than required %s\n' "$artifact_version" "$GOTOOLCHAIN_MINIMUM" >&2
    return 1
  fi
  printf 'built %s with %s\n' "$output" "$artifact_version"
}

if (( scan )) && [[ "$scan_ldflags" != "$ship_ldflags" ]]; then
  build_binary "$scan_ldflags"
  "$script_dir/scan-go-artifact.sh" "$output"
  build_binary "$ship_ldflags"
else
  build_binary "$ship_ldflags"
  if (( scan )); then
    "$script_dir/scan-go-artifact.sh" "$output"
  fi
fi
