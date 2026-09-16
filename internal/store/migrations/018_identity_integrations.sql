CREATE TABLE workspaces (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), email TEXT NOT NULL, display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN('owner','admin','member','viewer')),
  active BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_login_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX users_email_unique ON users(lower(email));
CREATE TABLE workspace_members (workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, role TEXT NOT NULL CHECK(role IN('owner','admin','member','viewer')), PRIMARY KEY(workspace_id,user_id));
CREATE TABLE user_sessions (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, csrf_token TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE api_tokens (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, prefix TEXT NOT NULL, expires_at TIMESTAMPTZ, last_used_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE audit_events (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID REFERENCES users(id) ON DELETE SET NULL, kind TEXT NOT NULL, resource_type TEXT NOT NULL DEFAULT '', resource_id TEXT NOT NULL DEFAULT '', metadata JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE integration_connections (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, provider TEXT NOT NULL CHECK(provider IN('github','gitlab','codeberg')), label TEXT NOT NULL, base_url TEXT NOT NULL DEFAULT '', account_login TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN('pending','connected','error','revoked')), credential_ciphertext BYTEA NOT NULL DEFAULT ''::bytea, credential_nonce BYTEA NOT NULL DEFAULT ''::bytea, last_synced_at TIMESTAMPTZ, last_error TEXT NOT NULL DEFAULT '', poll_cursor JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE project_sources (project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE, connection_id UUID NOT NULL REFERENCES integration_connections(id) ON DELETE CASCADE, remote_id TEXT NOT NULL, namespace TEXT NOT NULL, clone_url TEXT NOT NULL, PRIMARY KEY(project_id,connection_id), UNIQUE(connection_id,remote_id));
CREATE TABLE integration_deliveries (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), connection_id UUID NOT NULL REFERENCES integration_connections(id) ON DELETE CASCADE, provider_delivery_id TEXT NOT NULL, event_type TEXT NOT NULL, payload JSONB NOT NULL DEFAULT '{}'::jsonb, received_at TIMESTAMPTZ NOT NULL DEFAULT now(), processed_at TIMESTAMPTZ, error_message TEXT NOT NULL DEFAULT '', UNIQUE(connection_id,provider_delivery_id));
INSERT INTO workspaces(name) SELECT 'Default' WHERE NOT EXISTS (SELECT 1 FROM workspaces);
CREATE TRIGGER taskboard_live_users AFTER INSERT OR UPDATE OR DELETE ON users FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_integrations AFTER INSERT OR UPDATE OR DELETE ON integration_connections FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
