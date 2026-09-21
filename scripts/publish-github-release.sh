#!/usr/bin/env bash
# Bind a GitHub release tag to the exact commit the artifacts were built from.
# A concurrent merge of the approved branch cannot retarget the tag: the tag
# is created (or verified) at RELEASE_COMMIT before gh release create, and
# --target is the full SHA rather than a moving branch name.
set -euo pipefail

usage() {
  printf 'usage: %s [--gate-only]\n' "$(basename "$0")" >&2
}

approved_ref="${RELEASE_APPROVED_REF:-refs/heads/master}"
approved_branch="${RELEASE_APPROVED_BRANCH:-master}"
version="${RELEASE_VERSION:-}"
commit="${RELEASE_COMMIT:-}"
ref="${GITHUB_REF:-}"
repo="${GITHUB_REPOSITORY:-}"
dist="${RELEASE_DIST_DIR:-dist}"
gate_only=0

if [[ "${1:-}" == "--gate-only" ]]; then
  gate_only=1
elif [[ $# -gt 0 ]]; then
  usage
  exit 2
fi

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

normalize_sha() {
  printf '%s' "$1" | tr 'A-F' 'a-f'
}

validate_eligibility() {
  [[ -n "$version" ]] || fail "RELEASE_VERSION is required"
  [[ -n "$commit" ]] || fail "RELEASE_COMMIT is required"
  [[ -n "$ref" ]] || fail "GITHUB_REF is required"
  [[ -n "$repo" ]] || fail "GITHUB_REPOSITORY is required"
  if [[ "$ref" != "$approved_ref" ]]; then
    fail "ref ${ref} is not the approved release ref ${approved_ref}"
  fi
  if [[ ! "$commit" =~ ^[0-9a-fA-F]{40}$ ]]; then
    fail "RELEASE_COMMIT is not a full SHA-1: ${commit}"
  fi
  if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
    fail "RELEASE_VERSION is not a semantic version tag: ${version}"
  fi
  if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
    fail "GITHUB_REPOSITORY is malformed: ${repo}"
  fi
}

lookup_tag_commit() {
  gh api "repos/${repo}/commits/${version}" --jq .sha
}

verify_tag_commit() {
  local existing="$1"
  if [[ "$(normalize_sha "$existing")" != "$(normalize_sha "$commit")" ]]; then
    fail "release tag ${version} already points at ${existing}, not the built commit ${commit}"
  fi
}

create_or_verify_tag() {
  local existing=""
  if existing="$(lookup_tag_commit 2>/dev/null)" && [[ -n "$existing" ]]; then
    verify_tag_commit "$existing"
    printf 'release tag %s already bound to %s\n' "$version" "$commit"
    return
  fi
  if gh api --method POST "repos/${repo}/git/refs" \
    -f "ref=refs/tags/${version}" \
    -f "sha=${commit}" >/dev/null; then
    printf 'created immutable release tag %s at %s\n' "$version" "$commit"
    return
  fi
  existing="$(lookup_tag_commit)" || fail "failed to create or look up release tag ${version}"
  verify_tag_commit "$existing"
  printf 'release tag %s already bound to %s\n' "$version" "$commit"
}

release_exists() {
  gh release view "$version" --repo "$repo" >/dev/null 2>&1
}

publish_assets() {
  local assets=()
  shopt -s nullglob
  assets=("$dist"/*)
  shopt -u nullglob
  if (( ${#assets[@]} == 0 )); then
    fail "no release assets in ${dist}"
  fi
  if release_exists; then
    create_or_verify_tag
    gh release upload "$version" "${assets[@]}" --repo "$repo" --clobber
    printf 'refreshed identical release assets for %s at %s\n' "$version" "$commit"
    return
  fi
  create_or_verify_tag
  gh release create "$version" "${assets[@]}" \
    --repo "$repo" \
    --target "$commit" \
    --verify-tag \
    --title "Shipyard ${version}" \
    --generate-notes
  printf 'published release %s bound to %s\n' "$version" "$commit"
}

validate_eligibility
if (( gate_only )); then
  printf 'release eligibility ok: ref=%s commit=%s version=%s branch=%s\n' \
    "$ref" "$commit" "$version" "$approved_branch"
  exit 0
fi
publish_assets
