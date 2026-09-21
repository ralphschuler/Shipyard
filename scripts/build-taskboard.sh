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

ldflags="-X main.version=${build_version} -X main.commit=${build_commit} -X main.builtAt=${build_time} -X main.goversion=${go_version}"
if [[ -n "$extra_ldflags" ]]; then
  ldflags="${extra_ldflags} ${ldflags}"
fi

mkdir -p "$(dirname "$output")"
timeout "${TASKBOARD_BUILD_TIMEOUT:-120}s" go build -trimpath -ldflags "$ldflags" -o "$output" ./cmd/taskboard
chmod 0755 "$output"

artifact_version="$(shipyard_artifact_go_version "$output")"
if ! shipyard_go_version_ge "$artifact_version" "$GOTOOLCHAIN_MINIMUM"; then
  printf 'built artifact embeds %s, which is older than required %s\n' "$artifact_version" "$GOTOOLCHAIN_MINIMUM" >&2
  exit 1
fi
printf 'built %s with %s\n' "$output" "$artifact_version"

if (( scan )); then
  "$script_dir/scan-go-artifact.sh" "$output"
fi
