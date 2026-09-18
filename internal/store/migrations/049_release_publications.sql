CREATE TABLE IF NOT EXISTS release_publications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  repository_url TEXT NOT NULL,
  source_branch TEXT NOT NULL,
  target_branch TEXT NOT NULL,
  commit_sha TEXT NOT NULL,
  pr_number INTEGER NOT NULL CHECK (pr_number > 0),
  pr_url TEXT NOT NULL,
  comment_body TEXT NOT NULL,
  audit_recorded BOOLEAN NOT NULL DEFAULT FALSE,
  comment_recorded BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(task_id, project_id, run_id)
);

CREATE INDEX IF NOT EXISTS release_publications_task_idx
  ON release_publications(task_id, created_at DESC);
