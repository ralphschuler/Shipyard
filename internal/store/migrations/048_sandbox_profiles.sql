CREATE TABLE sandbox_profiles (
  name TEXT PRIMARY KEY,
  description TEXT NOT NULL,
  mounts JSONB NOT NULL DEFAULT '["worktree"]',
  network_mode TEXT NOT NULL CHECK(network_mode IN ('none','qa-network','bridge-only')),
  write_mode TEXT NOT NULL CHECK(write_mode IN ('worktree','readonly')),
  active BOOLEAN NOT NULL DEFAULT true
);
INSERT INTO sandbox_profiles(name,description,mounts,network_mode,write_mode) VALUES
 ('strict','Worktree-only, no network','["worktree"]','none','worktree'),
 ('development','Worktree-only development','["worktree"]','none','worktree'),
 ('qa-readonly','Read-only checks with explicit network policy','["worktree"]','none','readonly'),
 ('release-bridge','Host-side release bridge only','["worktree"]','bridge-only','readonly')
ON CONFLICT (name) DO NOTHING;
ALTER TABLE agents ADD COLUMN sandbox_profile TEXT NOT NULL DEFAULT 'strict' REFERENCES sandbox_profiles(name);
ALTER TABLE agent_runs ADD COLUMN sandbox_profile TEXT NOT NULL DEFAULT 'strict';
ALTER TABLE agent_runs ADD COLUMN sandbox_effective JSONB NOT NULL DEFAULT '{}';
