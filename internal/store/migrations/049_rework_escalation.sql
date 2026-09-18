ALTER TABLE tasks ADD COLUMN IF NOT EXISTS rework_count INTEGER NOT NULL DEFAULT 0 CHECK (rework_count >= 0);
CREATE INDEX IF NOT EXISTS tasks_rework_count ON tasks(rework_count) WHERE rework_count > 0;
CREATE TABLE IF NOT EXISTS task_rework_policies (
  id BOOLEAN PRIMARY KEY DEFAULT TRUE,
  version TEXT NOT NULL,
  policy JSONB NOT NULL DEFAULT '{}'::jsonb,
  human_escalation_after INTEGER NOT NULL DEFAULT 7 CHECK (human_escalation_after >= 0),
  budget_limit_microusd BIGINT NOT NULL DEFAULT 0 CHECK (budget_limit_microusd >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO task_rework_policies(id,version) VALUES(TRUE,'rework-v1') ON CONFLICT(id) DO NOTHING;
