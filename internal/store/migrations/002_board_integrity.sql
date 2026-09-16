-- A workflow column always belongs to the board named by a transition/task.
ALTER TABLE workflow_columns
  ADD CONSTRAINT workflow_columns_id_board_unique UNIQUE (id, board_id);

ALTER TABLE transitions
  ADD CONSTRAINT transitions_from_column_same_board
    FOREIGN KEY (from_column_id, board_id) REFERENCES workflow_columns(id, board_id),
  ADD CONSTRAINT transitions_to_column_same_board
    FOREIGN KEY (to_column_id, board_id) REFERENCES workflow_columns(id, board_id);

ALTER TABLE tasks
  ADD CONSTRAINT tasks_column_same_board
    FOREIGN KEY (column_id, board_id) REFERENCES workflow_columns(id, board_id);

CREATE INDEX IF NOT EXISTS task_comments_by_task_created ON task_comments(task_id, created_at);
CREATE INDEX IF NOT EXISTS task_transitions_by_task_created ON task_transitions(task_id, occurred_at DESC);
