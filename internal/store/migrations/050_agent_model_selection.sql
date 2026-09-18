-- Model and effort are agent policy, never provider policy. Keep the legacy
-- provider model column for API compatibility, but copy it once to agents so
-- existing profiles do not silently change their cost or capability.
ALTER TABLE agents
  ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS reasoning_effort TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS escalation_policy JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE agents a
SET model = p.model
FROM provider_settings p
WHERE a.model = '' AND p.provider = a.adapter AND p.model <> '';

UPDATE provider_settings
SET options = options - 'reasoning_effort'
WHERE provider = 'codex' AND options ? 'reasoning_effort';

ALTER TABLE agent_runs
  ADD COLUMN IF NOT EXISTS effective_model TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS effective_effort TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS escalation_stage TEXT NOT NULL DEFAULT '0',
  ADD COLUMN IF NOT EXISTS policy_version TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS discovery_source TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS fallback TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS budget_decision TEXT NOT NULL DEFAULT '';

ALTER TABLE provider_settings
  ADD COLUMN IF NOT EXISTS discovery_source TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS discovery_error TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS discovery_at TIMESTAMPTZ;
