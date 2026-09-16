#!/usr/bin/env bash
set -euo pipefail

# Every E2E execution receives a fresh namespace. Never reuse an existing
# board, workspace, worktree or repository as test input.
run_id="${1:-$(date -u +%Y%m%dT%H%M%SZ)}"
root="/home/agent/.taskboard-e2e/$run_id"
workspace="$root/workspace"
mkdir -p "$workspace"
git -C "$workspace" init -q
fixtures="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/e2e-fixtures"
cp "$fixtures/TODO_E2E_SPEC.md" "$workspace/TODO_E2E_SPEC.md"
cp "$fixtures/verify_todo_app.py" "$workspace/verify_todo_app.py"
chmod 0755 "$workspace/verify_todo_app.py"
git -C "$workspace" add TODO_E2E_SPEC.md verify_todo_app.py
git -C "$workspace" -c user.name=Taskboard -c user.email=taskboard@local commit -qm "Initialize isolated E2E $run_id"
printf '%s\n' "$run_id" "$workspace"
