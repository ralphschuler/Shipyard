#!/usr/bin/env bash
set -euo pipefail

# Read-only production readiness check for the local Shipyard deployment.
# It validates the running path and backup readability; it never writes to
# PostgreSQL, starts an agent, or alters a worktree.

database_url="${DATABASE_URL:-postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable}"
base_url="${TASKBOARD_VERIFY_URL:-https://codex.local}"
backup_dir="${TASKBOARD_BACKUP_DIR:-/home/agent/taskboard-backups}"
update_env_file="${TASKBOARD_ENV_FILE:-/etc/taskboard/taskboard.env}"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

for command in curl psql pg_restore systemctl stat date awk mktemp sha256sum cmp; do
	require "$command"
done
if [[ "${TASKBOARD_VERIFY_BWRAP:-1}" == "1" ]]; then
  "$(dirname "$0")/bubblewrap-healthcheck.sh"
fi

# The repository owns the hardened unit template, while activation under /etc
# is an explicit host-administration step. Report drift on every verification
# and let CI/operators make it a hard gate with TASKBOARD_REQUIRE_HARDENED_UNIT.
unit_template="$(dirname "$0")/taskboard.service"
installed_unit="/etc/systemd/system/taskboard.service"
service_unit_state="missing"
if [[ -r "$installed_unit" ]]; then
  if cmp -s "$unit_template" "$installed_unit"; then
    service_unit_state="current"
  else
    service_unit_state="drift"
  fi
fi
if [[ "${TASKBOARD_REQUIRE_HARDENED_UNIT:-0}" == "1" && "$service_unit_state" != "current" ]]; then
  printf 'installed systemd unit is %s; install deploy/taskboard.service and daemon-reload before accepting production\n' "$service_unit_state" >&2
  exit 1
fi

systemctl is-active --quiet taskboard
systemctl is-active --quiet nginx
curl --fail --silent --show-error --insecure "${base_url%/}/healthz" >/dev/null

if [[ "${TASKBOARD_VERIFY_UPDATE_CONFIG:-1}" == "1" ]]; then
  TASKBOARD_ENV_FILE="$update_env_file" "$(dirname "$0")/validate-update-config.sh"
  update_payload="$(curl --fail --silent --show-error --insecure "${base_url%/}/api/v1/settings/updates")"
  case "$update_payload" in
    *'"status":"update_available"'*|*'"status":"up_to_date"'*) ;;
    *)
      printf 'update check did not return a verified release status\n' >&2
      exit 1
      ;;
  esac
fi

# OpenAI tool calls are intentionally fail-closed unless bubblewrap supplies
# their filesystem and network boundary. Do not let a later settings change
# turn this into a runtime surprise after a deployment has been accepted.
openai_enabled="$(psql "$database_url" --tuples-only --no-align --command "SELECT enabled FROM provider_settings WHERE provider='openai'" | tr -d '[:space:]')"
if [[ "$openai_enabled" == "t" ]]; then
  require bwrap
fi

# A healthy process is not enough: protect the operator paths that are needed
# to inspect and recover the system. Deployments behind the local SSO bridge
# receive 200 here; installations with a separate login can opt out and run
# this check with an authenticated verifier instead.
if [[ "${TASKBOARD_VERIFY_UI:-1}" == "1" ]]; then
  # Reuse one browser session. Besides exercising the full local SSO path,
  # this prevents a verifier run from creating one durable login session per
  # route and turns every listed page into an authenticated 200 assertion.
  cookie_jar="$(mktemp)"
  cleanup_ui_verifier() { rm -f "$cookie_jar"; }
  trap cleanup_ui_verifier EXIT
  for path in / /boards /projects /agents /automations /schedules /webhooks /skills /runs /audit /settings/providers /settings/agent-policy /settings/appearance /settings/integrations /account; do
    curl --fail --silent --show-error --insecure --cookie "$cookie_jar" --cookie-jar "$cookie_jar" "${base_url%/}${path}" >/dev/null
  done
fi

read -r migrations pending active interactions stale_events stale_queued stale_running terminal_without_finished orphan_tasks completion_mismatches stalled_batches duplicate_special_columns stale_worktrees dead_webhooks stale_webhooks < <(
  psql "$database_url" --tuples-only --no-align --field-separator=' ' \
    --command "SELECT
      (SELECT count(*) FROM schema_migrations),
      (SELECT count(*) FROM automation_events WHERE processed_at IS NULL),
      (SELECT count(*) FROM agent_runs WHERE status IN ('queued','running')),
      (SELECT count(*) FROM agent_interactions WHERE status='open'),
      (SELECT count(*) FROM automation_events WHERE processed_at IS NULL AND occurred_at < now()-interval '10 minutes'),
      (SELECT count(*) FROM agent_runs WHERE status='queued' AND created_at < now()-interval '10 minutes'),
      (SELECT count(*) FROM agent_runs WHERE status='running' AND COALESCE(started_at,created_at) < now()-interval '25 minutes'),
      (SELECT count(*) FROM agent_runs WHERE status IN ('succeeded','failed','cancelled') AND finished_at IS NULL),
      (SELECT count(*) FROM tasks t LEFT JOIN workflow_columns c ON c.id=t.column_id AND c.board_id=t.board_id WHERE c.id IS NULL),
      (SELECT count(*) FROM tasks t JOIN workflow_columns c ON c.id=t.column_id WHERE (t.completed_at IS NOT NULL) <> (c.column_type='done')),
      (SELECT count(*) FROM agent_run_batches b WHERE b.status IN ('queued','running') AND NOT EXISTS (SELECT 1 FROM agent_runs r WHERE r.batch_id=b.id AND r.status IN ('queued','running'))),
      COALESCE((SELECT count(*) FROM (SELECT 1 FROM workflow_columns WHERE column_type <> 'standard' GROUP BY board_id,column_type HAVING count(*) > 1) duplicate_special_types),0),
      (SELECT count(*) FROM agent_runs WHERE worktree_path <> '' AND finished_at < now()-interval '8 days' AND (status IN ('failed','cancelled') OR gate_status='failed' OR applied_at IS NOT NULL)),
      (SELECT count(*) FROM webhook_deliveries WHERE status='dead'),
      (SELECT count(*) FROM webhook_deliveries WHERE status='sending' AND updated_at < now()-interval '2 minutes');"
)
expected_migrations="$(find "$(dirname "$0")/../internal/store/migrations" -maxdepth 1 -type f -name '*.sql' | wc -l | tr -d ' ')"
if [[ "$migrations" != "$expected_migrations" ]]; then
  printf 'migration mismatch: database=%s repository=%s\n' "$migrations" "$expected_migrations" >&2
  exit 1
fi
if (( stale_events > 0 || stale_queued > 0 || stale_running > 0 || terminal_without_finished > 0 || orphan_tasks > 0 || completion_mismatches > 0 || stalled_batches > 0 || duplicate_special_columns > 0 || stale_worktrees > 0 || dead_webhooks > 0 || stale_webhooks > 0 )); then
	printf 'run/workflow invariant failed: stale_events=%s stale_queued=%s stale_running=%s terminal_without_finished=%s orphan_tasks=%s completion_mismatches=%s stalled_batches=%s duplicate_special_columns=%s stale_worktrees=%s dead_webhooks=%s stale_webhooks=%s\n' \
		"$stale_events" "$stale_queued" "$stale_running" "$terminal_without_finished" "$orphan_tasks" "$completion_mismatches" "$stalled_batches" "$duplicate_special_columns" "$stale_worktrees" "$dead_webhooks" "$stale_webhooks" >&2
  exit 1
fi

latest_backup="$(find "$backup_dir" -maxdepth 1 -type f -name 'taskboard-*.dump' -printf '%T@ %p\n' | sort -n | tail -1 | cut -d' ' -f2-)"
if [[ -z "$latest_backup" ]]; then
  printf 'no PostgreSQL backup found in %s\n' "$backup_dir" >&2
  exit 1
fi
pg_restore --list "$latest_backup" >/dev/null
backup_metadata="${latest_backup%.dump}.meta"
if [[ ! -r "$backup_metadata" ]]; then
  printf 'backup metadata missing for %s\n' "$(basename "$latest_backup")" >&2
  exit 1
fi
backup_migrations="$(awk -F= '$1 == "schema_migrations" {print $2}' "$backup_metadata")"
if [[ "$backup_migrations" != "$migrations" ]]; then
  printf 'backup schema mismatch: backup=%s database=%s\n' "$backup_migrations" "$migrations" >&2
  exit 1
fi
backup_checksum="$(awk -F= '$1 == "sha256" {print $2}' "$backup_metadata")"
if [[ ! "$backup_checksum" =~ ^[[:xdigit:]]{64}$ ]]; then
  printf 'backup checksum missing or malformed for %s\n' "$(basename "$latest_backup")" >&2
  exit 1
fi
actual_checksum="$(sha256sum "$latest_backup" | awk '{print $1}')"
if [[ "$actual_checksum" != "$backup_checksum" ]]; then
  printf 'backup checksum mismatch for %s\n' "$(basename "$latest_backup")" >&2
  exit 1
fi
backup_age_seconds=$(( $(date +%s) - $(stat -c %Y "$latest_backup") ))
max_backup_age_seconds="${TASKBOARD_BACKUP_MAX_AGE_SECONDS:-93600}"
if (( backup_age_seconds < 0 || backup_age_seconds > max_backup_age_seconds )); then
  printf 'backup is stale: age=%ss maximum=%ss\n' "$backup_age_seconds" "$max_backup_age_seconds" >&2
  exit 1
fi

printf 'ready: migrations=%s pending_events=%s active_runs=%s open_interactions=%s backup=%s backup_age=%ss stale_events=%s stale_queued=%s stale_running=%s terminal_without_finished=%s orphan_tasks=%s completion_mismatches=%s stalled_batches=%s duplicate_special_columns=%s stale_worktrees=%s dead_webhooks=%s stale_webhooks=%s service_unit=%s\n' \
	"$migrations" "$pending" "$active" "$interactions" "$(basename "$latest_backup")" "$backup_age_seconds" "$stale_events" "$stale_queued" "$stale_running" "$terminal_without_finished" "$orphan_tasks" "$completion_mismatches" "$stalled_batches" "$duplicate_special_columns" "$stale_worktrees" "$dead_webhooks" "$stale_webhooks" "$service_unit_state"
