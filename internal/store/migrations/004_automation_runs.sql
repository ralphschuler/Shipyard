ALTER TABLE agent_runs ADD COLUMN event_id UUID REFERENCES automation_events(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX agent_runs_once_per_rule_event ON agent_runs(event_id, rule_id) WHERE event_id IS NOT NULL AND rule_id IS NOT NULL;
CREATE INDEX automation_events_pending ON automation_events(occurred_at) WHERE processed_at IS NULL;
