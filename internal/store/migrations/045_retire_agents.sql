-- Keep agent identities referenced by historical runs while removing retired
-- profiles from the active agent catalogue. This allows cleanup without
-- breaking run history, audit views or cost reporting.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ;
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS agents_active_name_key ON agents(name) WHERE retired_at IS NULL;
