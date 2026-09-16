CREATE TABLE agent_interactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
  agent_run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  schema JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'open' CHECK(status IN('open','answered','cancelled')),
  response JSONB,
  answered_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  answered_at TIMESTAMPTZ
);
CREATE INDEX agent_interactions_open ON agent_interactions(task_id,created_at DESC) WHERE status='open';
CREATE TRIGGER taskboard_live_interactions AFTER INSERT OR UPDATE OR DELETE ON agent_interactions FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
