-- Durable scheduling state for queued agent runs.
ALTER TABLE agent_runs
  ADD COLUMN IF NOT EXISTS queue_wait_started_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS queue_wait_reason TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS queue_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE agent_runs
SET queue_wait_started_at = COALESCE(queue_wait_started_at, created_at),
    queue_next_attempt_at = COALESCE(queue_next_attempt_at, created_at)
WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS agent_runs_queue_ready
  ON agent_runs(queue_next_attempt_at, created_at, id)
  WHERE status = 'queued';
