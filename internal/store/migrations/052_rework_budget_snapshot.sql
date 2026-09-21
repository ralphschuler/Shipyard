ALTER TABLE agent_runs
  ADD COLUMN IF NOT EXISTS budget_limit_microusd BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS selection_cost_microusd BIGINT NOT NULL DEFAULT 0;

UPDATE task_rework_policies
SET policy = '{
  "estimated_cost_microusd": {
    "luna/medium": 10,
    "luna/high": 20,
    "luna/xhigh": 30,
    "terra/medium": 30,
    "terra/high": 50,
    "terra/xhigh": 70,
    "soul/high": 100
  }
}'::jsonb
WHERE id = TRUE AND COALESCE(policy, '{}'::jsonb) = '{}'::jsonb;
