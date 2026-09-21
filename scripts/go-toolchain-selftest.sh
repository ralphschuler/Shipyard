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

# SIGPIPE / 141: under pipefail, an early-closing reader kills the writer.
# The live-pipe patterns below must keep returning 141 so we do not "fix"
# them by disabling pipefail. The helpers must return 0 after capturing first.
sigpipe_status=0
bash -c 'set -euo pipefail; yes | grep -q y' >/dev/null || sigpipe_status=$?
if [[ "$sigpipe_status" -ne 141 ]]; then
  fail "yes|grep -q under pipefail should exit 141 (SIGPIPE), got $sigpipe_status"
fi

work="$(mktemp -d)"
cleanup_work() { rm -rf "$work"; }
trap cleanup_work EXIT

help_stub="$work/govulncheck-help"
cat >"$help_stub" <<'EOF'
#!/usr/bin/env bash
printf '  -format string\n'
# 2MiB after the match: leftover bytes after grep's first read still fill the pipe.
head -c 2097152 /dev/zero | tr '\0' 'x'
printf '\n'
EOF
chmod +x "$help_stub"

help_pipe_status=0
bash -c 'set -euo pipefail; "$1" -h 2>&1 | grep -q -- "-format"' bash "$help_stub" >/dev/null || help_pipe_status=$?
if [[ "$help_pipe_status" -ne 141 ]]; then
  fail "padded help | grep -q under pipefail should exit 141, got $help_pipe_status"
fi
if ! shipyard_cmd_help_has_flag "$help_stub" '-format'; then
  fail "shipyard_cmd_help_has_flag missed -format under pipefail"
fi

padded_buildinfo="$work/buildinfo"
{
  printf '%s: go1.26.8\n' "$padded_buildinfo"
  head -c 2097152 /dev/zero | tr '\0' 'x'
  printf '\n'
} >"$padded_buildinfo"
awk_pipe_status=0
bash -c 'set -euo pipefail
  cat "$1" | awk "NR==1 {
    for (i = 1; i <= NF; i++) {
      if (\$i ~ /^go[0-9]/) { print \$i; exit }
    }
    exit 1
  }"
' bash "$padded_buildinfo" >/dev/null || awk_pipe_status=$?
if [[ "$awk_pipe_status" -ne 141 ]]; then
  fail "awk-exit on padded go version -m output should be 141, got $awk_pipe_status"
fi

go_bin="$(command -v go)"
version=""
version_status=0
version="$(shipyard_artifact_go_version "$go_bin")" || version_status=$?
if [[ "$version_status" -ne 0 ]]; then
  fail "shipyard_artifact_go_version exited $version_status under pipefail"
elif [[ "$version" != go1.* ]]; then
  fail "shipyard_artifact_go_version produced $version"
fi

if (( failures > 0 )); then
  exit 1
fi
printf 'go-toolchain self-test passed (minimum %s).\n' "$minimum"
