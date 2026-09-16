CREATE TABLE workflow_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  board_id UUID REFERENCES boards(id) ON DELETE SET NULL,
  root_task_id UUID REFERENCES tasks(id) ON DELETE SET NULL,
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','waiting_approval','succeeded','failed','cancelled')),
  idempotency_key TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE workflow_steps (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workflow_run_id UUID NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  step_key TEXT NOT NULL, task_id UUID REFERENCES tasks(id) ON DELETE SET NULL, agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','ready','running','waiting_approval','succeeded','failed','skipped','cancelled')),
  depends_on JSONB NOT NULL DEFAULT '[]', max_attempts INT NOT NULL DEFAULT 1 CHECK(max_attempts > 0), attempts INT NOT NULL DEFAULT 0,
  prompt_snapshot TEXT NOT NULL DEFAULT '', output_summary TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workflow_run_id,step_key)
);
CREATE TABLE workflow_approvals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workflow_step_id UUID NOT NULL REFERENCES workflow_steps(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')), requested_at TIMESTAMPTZ NOT NULL DEFAULT now(), decided_at TIMESTAMPTZ, note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE orchestration_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workflow_run_id UUID NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  workflow_step_id UUID REFERENCES workflow_steps(id) ON DELETE CASCADE, event_type TEXT NOT NULL, provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', attributes JSONB NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX workflow_steps_runnable ON workflow_steps(workflow_run_id,status);
CREATE INDEX orchestration_events_by_run ON orchestration_events(workflow_run_id,created_at);
CREATE TRIGGER taskboard_live_workflow_runs AFTER INSERT OR UPDATE OR DELETE ON workflow_runs FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_workflow_steps AFTER INSERT OR UPDATE OR DELETE ON workflow_steps FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_approvals AFTER INSERT OR UPDATE OR DELETE ON workflow_approvals FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_orchestration_events AFTER INSERT OR UPDATE OR DELETE ON orchestration_events FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
