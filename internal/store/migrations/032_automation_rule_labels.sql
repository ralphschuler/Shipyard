-- A lifecycle rule may be scoped to a task label. This makes specialist
-- routing deterministic: moving a task to a column does not fan out to every
-- agent, only to the matching discipline.
ALTER TABLE automation_rules
  ADD COLUMN label_id UUID REFERENCES labels(id) ON DELETE SET NULL;

CREATE INDEX automation_rules_label_id_idx ON automation_rules(label_id)
  WHERE label_id IS NOT NULL;
