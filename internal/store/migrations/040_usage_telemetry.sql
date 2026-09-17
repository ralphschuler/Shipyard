-- Usage is nullable on purpose: NULL means the adapter did not provide a value.
-- Keep the legacy integer columns for older views and API clients.
ALTER TABLE agent_runs
  ADD COLUMN IF NOT EXISTS usage_provider TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS usage_model TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS usage_service_tier TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS usage_api_calls INTEGER,
  ADD COLUMN IF NOT EXISTS usage_input_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS usage_output_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS usage_cached_input_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS usage_cache_write_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS usage_reasoning_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS usage_total_tokens BIGINT,
  ADD COLUMN IF NOT EXISTS usage_status TEXT NOT NULL DEFAULT 'unknown' CHECK (usage_status IN ('complete','incomplete','unknown')),
  ADD COLUMN IF NOT EXISTS native_cost_microusd BIGINT,
  ADD COLUMN IF NOT EXISTS raw_usage JSONB,
  ADD COLUMN IF NOT EXISTS cost_source TEXT NOT NULL DEFAULT 'unknown' CHECK (cost_source IN ('reported','estimated','included','unknown')),
  ADD COLUMN IF NOT EXISTS calculated_cost_microusd BIGINT,
  ADD COLUMN IF NOT EXISTS price_version TEXT,
  ADD COLUMN IF NOT EXISTS cost_calculated_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS usage_price_catalog (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  service_tier TEXT NOT NULL DEFAULT '',
  valid_from TIMESTAMPTZ NOT NULL,
  valid_until TIMESTAMPTZ,
  version TEXT NOT NULL,
  input_microusd_per_million BIGINT,
  output_microusd_per_million BIGINT,
  cached_input_microusd_per_million BIGINT,
  cache_write_microusd_per_million BIGINT,
  reasoning_microusd_per_million BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (valid_until IS NULL OR valid_until > valid_from),
  UNIQUE(provider, model, service_tier, valid_from, version)
);
-- A model/tier may have only one applicable price at a point in time. The
-- constraint is database-enforced so admin/API clients cannot create
-- ambiguous historical resolutions through a race.
CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE usage_price_catalog
  DROP CONSTRAINT IF EXISTS usage_price_catalog_no_overlap;
ALTER TABLE usage_price_catalog
  ADD CONSTRAINT usage_price_catalog_no_overlap
  EXCLUDE USING gist (
    provider WITH =,
    model WITH =,
    service_tier WITH =,
    tstzrange(valid_from, COALESCE(valid_until, 'infinity'::timestamptz), '[)') WITH &&
  );
CREATE INDEX IF NOT EXISTS usage_price_catalog_lookup ON usage_price_catalog(provider, model, service_tier, valid_from, valid_until);
