#!/usr/bin/env bash
# Shared helpers for the patched Go toolchain required by Shipyard builds.
# Sourced by require/build/scan scripts; not intended to be executed directly.
set -euo pipefail

_SHIPYARD_TOOLCHAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

shipyard_repo_root() {
  printf '%s\n' "$(cd "$_SHIPYARD_TOOLCHAIN_DIR/.." && pwd)"
}

shipyard_minimum_go_toolchain() {
  local go_mod="${1:-$(shipyard_repo_root)/go.mod}"
  if [[ ! -r "$go_mod" ]]; then
    printf 'go.mod is not readable at %s\n' "$go_mod" >&2
    return 1
  fi
  local declared
  declared="$(awk '
    $1 == "toolchain" { toolchain = $2 }
    $1 == "go" { go_line = $2 }
    END {
      if (toolchain != "") { print toolchain; exit }
      if (go_line == "") { exit 1 }
      if (go_line ~ /^go/) { print go_line; exit }
      print "go" go_line
    }
  ' "$go_mod")"
  if [[ -z "$declared" ]]; then
    printf 'go.mod does not declare a Go toolchain\n' >&2
    return 1
  fi
  printf '%s\n' "$declared"
}

shipyard_normalize_go_version() {
  local raw="${1:-}"
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  raw="${raw#go}"
  raw="${raw%% *}"
  if [[ "$raw" =~ ^([0-9]+)\.([0-9]+)(\.([0-9]+))? ]]; then
    printf 'go%s.%s.%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[4]:-0}"
    return 0
  fi
  printf 'invalid Go version %q\n' "${1:-}" >&2
  return 1
}

shipyard_go_version_ge() {
  local got need newest
  got="$(shipyard_normalize_go_version "$1")"
  need="$(shipyard_normalize_go_version "$2")"
  newest="$(printf '%s\n%s\n' "${got#go}" "${need#go}" | sort -V | tail -n 1)"
  [[ "$newest" == "${got#go}" ]]
}

shipyard_selected_go_version() {
  local version
  version="$(go env GOVERSION 2>/dev/null || true)"
  if [[ -z "$version" ]]; then
    version="$(go version | awk '{print $3}')"
  fi
  printf '%s\n' "$version"
}

# Capture command output, then search it. grep -q / awk-exit on a live pipe
# under pipefail is SIGPIPE (141) when the writer still has unread bytes.
shipyard_cmd_help_has_flag() {
  local bin="$1"
  local flag="$2"
  local help=""
  help="$("$bin" -h 2>&1)" || true
  if grep -Fq -- "$flag" <<<"$help"; then
    return 0
  fi
  return 1
}

shipyard_artifact_go_version() {
  local binary="$1"
  if [[ ! -e "$binary" ]]; then
    printf 'artifact %s does not exist\n' "$binary" >&2
    return 1
  fi
  local buildinfo version
  # `go version -m` prints one header line then every module. awk-exit on that
  # live pipe under pipefail aborts the release with 141 before "built ...".
  buildinfo="$(go version -m "$binary")"
  version="$(awk 'NR==1 {
    for (i = 1; i <= NF; i++) {
      if ($i ~ /^go[0-9]/) { print $i; exit }
    }
    exit 1
  }' <<<"$buildinfo")"
  printf '%s\n' "$version"
}

shipyard_require_patched_go() {
  local minimum selected
  minimum="$(shipyard_minimum_go_toolchain "${1:-}")"
  selected="${2:-$(shipyard_selected_go_version)}"
  if ! shipyard_go_version_ge "$selected" "$minimum"; then
    printf 'Go toolchain %s is not a supported patched release; need %s or later. See https://go.dev/doc/devel/release\n' "$selected" "$minimum" >&2
    return 1
  fi
  printf 'using patched Go toolchain %s (minimum %s)\n' "$selected" "$minimum"
}

shipyard_export_build_toolchain() {
  local minimum
  minimum="$(shipyard_minimum_go_toolchain "${1:-}")"
  export GOTOOLCHAIN="$minimum"
  export GOTOOLCHAIN_MINIMUM="$minimum"
}
