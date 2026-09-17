ALTER TABLE agent_runs
  ADD COLUMN IF NOT EXISTS integration_branch TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS integration_base_sha TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS integration_head_sha TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS integration_status TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS pr_url TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS pr_number INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS repository_integration_queue (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  repository_path TEXT NOT NULL,
  run_id UUID NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  branch TEXT NOT NULL,
  default_branch TEXT NOT NULL,
  base_sha TEXT NOT NULL DEFAULT '',
  head_sha TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','pushed','pr_open','succeeded','failed')),
  step TEXT NOT NULL DEFAULT 'fetch',
  pr_url TEXT NOT NULL DEFAULT '',
  pr_number INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE repository_integration_queue
  ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS repository_integration_queue_pending
  ON repository_integration_queue(repository_path, next_attempt_at, created_at)
  WHERE status IN ('queued','running','pushed','pr_open');
CREATE INDEX IF NOT EXISTS repository_integration_queue_claim
  ON repository_integration_queue(repository_path, next_attempt_at, claimed_until, created_at)
  WHERE status IN ('queued','running','pushed','pr_open');
