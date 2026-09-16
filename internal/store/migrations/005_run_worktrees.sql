ALTER TABLE agent_runs ADD COLUMN worktree_path TEXT NOT NULL DEFAULT '';
CREATE INDEX agent_runs_active_workspace ON agent_runs(workspace_snapshot) WHERE status IN ('queued','running');
