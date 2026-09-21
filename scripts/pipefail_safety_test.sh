#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
ec=0
bash -c 'set -euo pipefail; yes | grep -q y' || ec=$?
if [[ "$ec" -ne 141 ]]; then
  printf 'expected yes|grep -q to exit 141 under pipefail, got %s\n' "$ec" >&2
  exit 1
fi
# Strip comments/strings-ish: flag real pipeline uses of grep -q
if grep -nE '^[^#]*\|\s*grep -q' "$root/scripts/scan-go-artifact.sh"; then
  printf 'scan-go-artifact.sh still pipes into grep -q\n' >&2
  exit 1
fi
if grep -nE '^[^#]*go version -m .*\|\s*awk' "$root/scripts/go-toolchain.sh"; then
  printf 'go-toolchain.sh still pipes go version -m into awk\n' >&2
  exit 1
fi
# Helper must work under pipefail
source "$root/scripts/go-toolchain.sh"
tmp="$(mktemp)"
printf 'package main\nfunc main(){}\n' >"$tmp.go"
go build -o "$tmp" "$tmp.go"
shipyard_artifact_go_version "$tmp" >/dev/null
rm -f "$tmp" "$tmp.go"
printf 'pipefail safety checks passed\n'
