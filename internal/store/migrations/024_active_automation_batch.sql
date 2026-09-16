-- A task/rule pair may have several historical batches but only one current
-- batch. This prevents repeated transitions from launching duplicate agents.
CREATE UNIQUE INDEX IF NOT EXISTS agent_run_batches_one_active_automation
ON agent_run_batches(task_id, rule_id)
WHERE rule_id IS NOT NULL AND status IN ('queued','running');
