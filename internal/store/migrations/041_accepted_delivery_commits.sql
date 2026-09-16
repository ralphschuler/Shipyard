-- A commit subject is user-controlled and cannot prove that a delivery was
-- accepted. Keep the exact Git object recorded at the acceptance boundary.
ALTER TABLE agent_runs
  ADD COLUMN IF NOT EXISTS accepted_commit_sha TEXT;

CREATE INDEX IF NOT EXISTS agent_runs_accepted_commit_source_idx
  ON agent_runs(source_workspace, accepted_commit_sha)
  WHERE accepted_commit_sha IS NOT NULL;
