-- Successful automation output remains in review until each target patch was
-- explicitly accepted. The marker makes the resulting success transition
-- exactly-once for a fan-out batch.
ALTER TABLE agent_run_batches ADD COLUMN IF NOT EXISTS delivery_handled_at TIMESTAMPTZ;
