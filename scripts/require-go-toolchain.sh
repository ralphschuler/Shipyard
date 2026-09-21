#!/usr/bin/env bash
# Fail unless the selected (or supplied) Go toolchain is a supported patched release.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=go-toolchain.sh
source "$script_dir/go-toolchain.sh"

version=""
go_mod="$(shipyard_repo_root)/go.mod"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --go-mod)
      go_mod="${2:-}"
      shift 2
      ;;
    *)
      printf 'usage: %s [--version go1.X.Y] [--go-mod path]\n' "$0" >&2
      exit 2
      ;;
  esac
done

shipyard_export_build_toolchain "$go_mod"
shipyard_require_patched_go "$go_mod" "${version:-$(shipyard_selected_go_version)}"
