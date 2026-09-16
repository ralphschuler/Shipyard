-- Existing boards often used a visible "Blocked" or decision column before
-- semantic types existed. Preserve that intent for agent routing.
UPDATE workflow_columns
SET column_type = 'needs_action'
WHERE column_type = 'standard'
  AND lower(trim(name)) IN ('blocked', 'needs action', 'needs_action', 'needs decision', 'entscheidung');
