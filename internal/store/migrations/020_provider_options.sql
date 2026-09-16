ALTER TABLE provider_settings ADD COLUMN options JSONB NOT NULL DEFAULT '{}'::jsonb;
