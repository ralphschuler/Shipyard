#!/usr/bin/env bash
# Best-effort: make the GHCR agent base package public after the image push.
# Agent hosts pull this image without a registry login, and a package published
# by GITHUB_TOKEN starts private until its visibility is changed.
# The Packages visibility route 404s for some owners (personal accounts in
# particular) even after the package is registered. That must not fail the
# Release workflow; the GitHub release still publishes the binaries, and a
# host can pull a private package with a registry login.
set -euo pipefail

package_name="${AGENT_BASE_PACKAGE:-shipyard-agent-base}"
owner="${GITHUB_REPOSITORY_OWNER:-}"
attempts="${VISIBILITY_ATTEMPTS:-6}"
sleep_scale="${VISIBILITY_SLEEP_SCALE:-1}"

warn() {
  local message="$*"
  message="${message//$'\n'/ }"
  printf '::warning::%s\n' "$message"
  printf '%s\n' "$message" >&2
}

urlencode() {
  local value="$1" i c
  local LC_ALL=C
  for (( i = 0; i < ${#value}; i++ )); do
    c="${value:i:1}"
    case "$c" in
      [a-zA-Z0-9._~-]) printf '%s' "$c" ;;
      *) printf '%%%02X' "'$c" ;;
    esac
  done
}

if [[ ! "$attempts" =~ ^[1-9][0-9]*$ ]]; then
  attempts=6
fi
if [[ ! "$sleep_scale" =~ ^[0-9]+$ ]]; then
  sleep_scale=1
fi

if [[ -z "$owner" ]]; then
  warn "GITHUB_REPOSITORY_OWNER is unset; skipped making the agent base package public"
  exit 0
fi

image="ghcr.io/${owner}/${package_name}"
encoded="$(urlencode "$package_name")"
owner_type="$(gh api "users/${owner}" --jq .type 2>/dev/null || true)"
if [[ "$owner_type" == "Organization" ]]; then
  package_path="/orgs/${owner}/packages/container/${encoded}"
else
  package_path="/users/${owner}/packages/container/${encoded}"
fi
printf 'package API path %s (owner type %s)\n' "$package_path" "${owner_type:-unknown}"

visibility=""
for (( attempt = 1; attempt <= attempts; attempt++ )); do
  if visibility="$(gh api "$package_path" --jq .visibility 2>/dev/null)" && [[ -n "$visibility" && "$visibility" != "null" ]]; then
    break
  fi
  visibility=""
  if [[ "$attempt" -lt "$attempts" && "$sleep_scale" != "0" ]]; then
    sleep $(( attempt * sleep_scale ))
  fi
done

if [[ -z "$visibility" ]]; then
  warn "package ${image} was not found at ${package_path} after the push. The GitHub release will still publish. Anonymous image pulls need a public package."
  exit 0
fi
if [[ "$visibility" == "public" ]]; then
  printf 'package %s is public\n' "$image"
  exit 0
fi

err=""
if err="$(gh api --method PUT \
  -H "Accept: application/vnd.github+json" \
  "${package_path}/visibility" \
  -f visibility=public 2>&1)"; then
  printf 'made %s public\n' "$image"
  exit 0
fi
warn "could not make ${image} public via ${package_path}/visibility (${err}). The GitHub release will still publish. Anonymous image pulls need a public package."
exit 0
