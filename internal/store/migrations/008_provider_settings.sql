CREATE TABLE provider_settings (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), provider TEXT UNIQUE NOT NULL, enabled BOOLEAN NOT NULL DEFAULT false, model TEXT NOT NULL, command TEXT NOT NULL DEFAULT '', secret_env TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
INSERT INTO provider_settings(provider,model,command,secret_env) VALUES
('openai','gpt-5.6-luna','','OPENAI_API_KEY'),('codex','gpt-5.6-luna','codex exec',''),('claude','claude-sonnet-4-6','claude','ANTHROPIC_API_KEY') ON CONFLICT(provider) DO NOTHING;
