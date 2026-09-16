ALTER TABLE automation_events ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE automation_events ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
CREATE INDEX agent_runs_workspace_active ON agent_runs(workspace_snapshot) WHERE status IN ('queued','running');
