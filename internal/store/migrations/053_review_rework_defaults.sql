-- Review agents created from the original templates left defective work in
-- Review. Replace those exact default prompts with the rework contract.
-- Custom operator prompts are not rewritten.
UPDATE agents
SET prompt = 'Review the change critically against the task acceptance criteria and document concrete issues. Do not create a push, merge, or release. If defects block acceptance, emit self-review status=failed and exactly one taskboard-transition to the board''s rework target (Entwicklung / "Überarbeiten"). Never leave defective work in Review for a human by default. If the review passes, emit status=passed and do not auto-transition to Erledigt.',
    description = CASE
      WHEN description IN ('Code-Review-Agent', 'Code review agent') THEN 'Code review agent'
      ELSE description
    END
WHERE retired_at IS NULL
  AND prompt IN (
    'Prüfe die Änderung kritisch und dokumentiere konkrete Probleme. Erstelle keinen Push, Merge oder Release.',
    'Review the change critically and document concrete issues. Do not create a push, merge, or release.'
  );

-- Safety net: Review automations that fire on the Review column and have no
-- FailureColumn get the board's Überarbeiten/Entwicklung (or In Progress)
-- return edge. Operator-configured failure columns are left unchanged.
UPDATE automation_rules r
SET failure_column_id = (
  SELECT tr.to_column_id
  FROM transitions tr
  JOIN workflow_columns dest ON dest.id = tr.to_column_id
  WHERE tr.board_id = review_col.board_id
    AND tr.from_column_id = review_col.id
    AND dest.column_type <> 'done'
    AND (
      lower(tr.action_name) IN ('überarbeiten', 'uberarbeiten', 'nacharbeit anfordern')
      OR lower(dest.name) IN ('entwicklung', 'in progress')
    )
  ORDER BY
    CASE
      WHEN lower(tr.action_name) IN ('überarbeiten', 'uberarbeiten', 'nacharbeit anfordern') THEN 0
      WHEN lower(dest.name) IN ('entwicklung', 'in progress') THEN 1
      ELSE 2
    END,
    dest.position
  LIMIT 1
)
FROM agents a, workflow_columns review_col
WHERE r.agent_id = a.id
  AND review_col.id = r.target_column_id
  AND r.failure_column_id IS NULL
  AND lower(review_col.name) = 'review'
  AND (
    lower(a.name) = 'review agent'
    OR a.name ILIKE 'review agent %'
    OR lower(a.description) IN ('code review agent', 'code-review-agent')
  )
  AND EXISTS (
    SELECT 1
    FROM transitions tr
    JOIN workflow_columns dest ON dest.id = tr.to_column_id
    WHERE tr.board_id = review_col.board_id
      AND tr.from_column_id = review_col.id
      AND dest.column_type <> 'done'
      AND (
        lower(tr.action_name) IN ('überarbeiten', 'uberarbeiten', 'nacharbeit anfordern')
        OR lower(dest.name) IN ('entwicklung', 'in progress')
      )
  );
