-- Keep session cleanup fast as local SSO can create a session after a browser
-- profile reset. The store transaction retains the newest 15 per user before
-- creating the next session and removes expired rows globally.
CREATE INDEX IF NOT EXISTS user_sessions_expires_at_idx ON user_sessions(expires_at);
CREATE INDEX IF NOT EXISTS user_sessions_user_created_idx ON user_sessions(user_id, created_at DESC, id DESC);
