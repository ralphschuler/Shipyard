ALTER TABLE automation_rules ADD COLUMN success_column_id UUID REFERENCES workflow_columns(id) ON DELETE SET NULL;
ALTER TABLE automation_rules ADD COLUMN failure_column_id UUID REFERENCES workflow_columns(id) ON DELETE SET NULL;
CREATE TABLE notifications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID REFERENCES tasks(id) ON DELETE CASCADE,
  agent_run_id UUID REFERENCES agent_runs(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  message TEXT NOT NULL,
  read_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_unread ON notifications(created_at DESC) WHERE read_at IS NULL;
