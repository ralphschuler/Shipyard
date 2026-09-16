-- Semantic workflow roles are stable API contracts for automations and MCP.
ALTER TABLE workflow_columns
  ADD COLUMN column_type TEXT NOT NULL DEFAULT 'standard'
  CHECK (column_type IN ('standard', 'inbox', 'done', 'needs_action'));

-- Keep existing workflows functional while moving away from display-name based logic.
UPDATE workflow_columns SET column_type = 'inbox' WHERE is_initial;
UPDATE workflow_columns SET column_type = 'done' WHERE is_terminal;

CREATE UNIQUE INDEX one_special_workflow_column_type_per_board
  ON workflow_columns(board_id, column_type)
  WHERE column_type <> 'standard';
