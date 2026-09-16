DROP INDEX IF EXISTS agent_runs_once_per_rule_event;
CREATE UNIQUE INDEX agent_runs_once_per_rule_event_target ON agent_runs(event_id,rule_id,COALESCE(target_project_id,'00000000-0000-0000-0000-000000000000'::uuid)) WHERE event_id IS NOT NULL AND rule_id IS NOT NULL;
