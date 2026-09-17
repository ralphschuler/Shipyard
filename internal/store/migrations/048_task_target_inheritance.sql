ALTER TABLE task_repository_targets
  ADD COLUMN target_source TEXT NOT NULL DEFAULT 'explicit'
  CHECK (target_source IN ('explicit', 'inherited'));

CREATE INDEX task_repository_targets_inherited
  ON task_repository_targets(task_id) WHERE target_source='inherited';
