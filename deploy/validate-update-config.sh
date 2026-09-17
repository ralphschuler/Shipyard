#!/usr/bin/env bash
# Validate the external GitHub update-check configuration without printing
# secrets. The service reads the same file through systemd EnvironmentFile.
set -euo pipefail

env_file="${TASKBOARD_ENV_FILE:-/etc/taskboard/taskboard.env}"
if [[ ! -r "$env_file" ]]; then
  printf 'update configuration missing: create %s from deploy/taskboard.env.example and configure the repository, branch, and release policy\n' "$env_file" >&2
  exit 1
fi

mode="$(stat -c '%a' "$env_file")"
case "$mode" in
  600|640) ;;
  *)
    printf 'update configuration must be mode 600 or 640 (got %s)\n' "$mode" >&2
    exit 1
    ;;
esac

read_value() {
  local key="$1"
  awk -v key="$key" '
    $0 ~ "^[[:space:]]*" key "=" {
      sub("^[[:space:]]*" key "=", "")
      gsub(/^"|"$/, "")
      print
      exit
    }
  ' "$env_file"
}

repository="$(read_value TASKBOARD_GITHUB_REPOSITORY)"
branch="$(read_value TASKBOARD_GITHUB_BRANCH)"
token="$(read_value TASKBOARD_GITHUB_TOKEN)"
allowlist="$(read_value TASKBOARD_GITHUB_RELEASE_ALLOWLIST)"

if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  printf 'TASKBOARD_GITHUB_REPOSITORY is missing or malformed (expected owner/name)\n' >&2
  exit 1
fi
if [[ -z "$branch" || "$branch" == *$'\n'* ]]; then
  printf 'TASKBOARD_GITHUB_BRANCH is missing or malformed\n' >&2
  exit 1
fi
if [[ "$token" == *$'\n'* ]]; then
  printf 'TASKBOARD_GITHUB_TOKEN is malformed\n' >&2
  exit 1
fi
if [[ -z "${allowlist//[[:space:]]/}" ]]; then
  printf 'TASKBOARD_GITHUB_RELEASE_ALLOWLIST is missing; configure exact tags or a patch prefix such as v0.1.*\n' >&2
  exit 1
fi

entry_count=0
IFS=',' read -r -a entries <<< "$allowlist"
for entry in "${entries[@]}"; do
  entry="$(printf '%s' "$entry" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  [[ -z "$entry" ]] && continue
  entry_count=$((entry_count + 1))
  if [[ ! "$entry" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ && ! "$entry" =~ ^v?[0-9]+\.[0-9]+\.\*$ ]]; then
    printf 'TASKBOARD_GITHUB_RELEASE_ALLOWLIST contains a malformed entry\n' >&2
    exit 1
  fi
done
if (( entry_count == 0 )); then
  printf 'TASKBOARD_GITHUB_RELEASE_ALLOWLIST has no usable entries\n' >&2
  exit 1
fi

token_state="optional-not-configured"
if [[ -n "$token" ]]; then
  token_state="present"
fi
printf 'update configuration valid: repository=%s branch=%s policy_entries=%s token=%s\n' "$repository" "$branch" "$entry_count" "$token_state"
