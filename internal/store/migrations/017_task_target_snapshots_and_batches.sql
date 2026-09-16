CREATE TABLE task_repository_targets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
  project_name TEXT NOT NULL,
  repository_url TEXT NOT NULL,
  default_branch TEXT NOT NULL,
  local_path TEXT NOT NULL,
  source_groups JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(task_id, project_id)
);
CREATE TABLE agent_run_batches (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
  rule_id UUID REFERENCES automation_rules(id) ON DELETE SET NULL,
  event_id UUID REFERENCES automation_events(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN('queued','running','succeeded','failed','cancelled','partial')),
  total_targets INTEGER NOT NULL DEFAULT 0,
  succeeded_targets INTEGER NOT NULL DEFAULT 0,
  failed_targets INTEGER NOT NULL DEFAULT 0,
  cancelled_targets INTEGER NOT NULL DEFAULT 0,
  outcome_handled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE agent_runs ADD COLUMN batch_id UUID REFERENCES agent_run_batches(id) ON DELETE SET NULL;

-- Existing assignments become a point-in-time snapshot as soon as this
-- migration is applied. Subsequent group membership changes never rewrite it.
INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups)
SELECT t.task_id,p.id,p.name,p.repository_url,p.default_branch,p.local_path,'[]'::jsonb
FROM task_target_projects t JOIN projects p ON p.id=t.project_id
ON CONFLICT (task_id,project_id) DO NOTHING;
INSERT INTO task_repository_targets(task_id,project_id,project_name,repository_url,default_branch,local_path,source_groups)
SELECT t.task_id,p.id,p.name,p.repository_url,p.default_branch,p.local_path,
       jsonb_agg(jsonb_build_object('id',g.id,'name',g.name,'color',g.color))
FROM task_target_groups t
JOIN project_groups g ON g.id=t.group_id
JOIN project_group_members m ON m.group_id=g.id
JOIN projects p ON p.id=m.project_id
GROUP BY t.task_id,p.id,p.name,p.repository_url,p.default_branch,p.local_path
ON CONFLICT (task_id,project_id) DO UPDATE SET source_groups=EXCLUDED.source_groups;
CREATE INDEX task_repository_targets_task ON task_repository_targets(task_id);
CREATE INDEX agent_runs_batch ON agent_runs(batch_id);
CREATE TRIGGER taskboard_live_task_repository_targets AFTER INSERT OR UPDATE OR DELETE ON task_repository_targets FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_run_batches AFTER INSERT OR UPDATE OR DELETE ON agent_run_batches FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
