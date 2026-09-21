#!/usr/bin/env bash
# Scan a finished Shipyard artifact with the toolchain that built it.
# Call-path hits and binary symbol hits fail the gate; they are static matches,
# not proven exploits. Package/module-only hints are reported and do not fail.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=go-toolchain.sh
source "$script_dir/go-toolchain.sh"

if [[ $# -ne 1 ]]; then
  printf 'usage: %s <binary>\n' "$0" >&2
  exit 2
fi

binary="$1"
repo_root="$(shipyard_repo_root)"
cd "$repo_root"

# Cross-release jobs set GOOS/GOARCH for the artifact. Source analysis and the
# classifier must use the host toolchain instead of trying to execute a
# foreign GOARCH binary.
unset GOOS GOARCH

if [[ ! -e "$binary" ]]; then
  printf 'artifact %s does not exist\n' "$binary" >&2
  exit 1
fi

shipyard_export_build_toolchain
artifact_version="$(shipyard_artifact_go_version "$binary")"
shipyard_require_patched_go "$(shipyard_repo_root)/go.mod" "$artifact_version"

# Force govulncheck to analyze the stdlib of the artifact, not a newer local
# toolchain that would hide production risk.
export GOTOOLCHAIN="$artifact_version"

govulncheck_bin="${TASKBOARD_GOVULNCHECK:-}"
if [[ -z "$govulncheck_bin" ]]; then
  gobin="$(mktemp -d)"
  cleanup_gobin() { rm -rf "$gobin"; }
  trap cleanup_gobin EXIT
  GOBIN="$gobin" timeout "${TASKBOARD_GOVULNCHECK_INSTALL_TIMEOUT:-180}s" go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
  govulncheck_bin="$gobin/govulncheck"
fi

work="$(mktemp -d)"
cleanup_work() { rm -rf "$work" ${gobin:+"$gobin"}; }
trap cleanup_work EXIT

source_json="$work/source.json"
binary_json="$work/binary.json"

run_govulncheck_json() {
  local output="$1"
  shift
  set +e
  if "$govulncheck_bin" -h 2>&1 | grep -q -- '-format'; then
    timeout "${TASKBOARD_GOVULNCHECK_TIMEOUT:-180}s" "$govulncheck_bin" -format=json "$@" >"$output"
  else
    timeout "${TASKBOARD_GOVULNCHECK_TIMEOUT:-180}s" "$govulncheck_bin" -json "$@" >"$output"
  fi
  local status=$?
  set -e
  # 0 = clean, 3 = findings. Any other status is a scanner failure.
  if [[ "$status" -ne 0 && "$status" -ne 3 ]]; then
    printf 'govulncheck failed with exit %s\n' "$status" >&2
    return 1
  fi
  return 0
}

run_govulncheck_json "$binary_json" -mode=binary "$binary"
run_govulncheck_json "$source_json" ./...

set +e
timeout "${TASKBOARD_GOVULNCHECK_TIMEOUT:-60}s" go run ./cmd/govulngate \
  -go-version "$artifact_version" \
  -source-json "$source_json" \
  -binary-json "$binary_json"
status=$?
set -e

if [[ "$status" -eq 3 ]]; then
  printf 'vulnerability gate failed: call-path or symbol hits were found in the scanned artifact/toolchain. These are static advisory matches, not proven exploits.\n' >&2
  exit 1
fi
if [[ "$status" -ne 0 ]]; then
  printf 'vulnerability gate could not classify govulncheck results (exit %s)\n' "$status" >&2
  exit 1
fi

printf 'vulnerability gate passed for %s (%s)\n' "$binary" "$artifact_version"
