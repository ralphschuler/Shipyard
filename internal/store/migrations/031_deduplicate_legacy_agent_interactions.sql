-- Runs created before stable interaction keys could leave several identical
-- questions open. Backfill a deterministic legacy identity, retain history,
-- and close only redundant unresolved records. The partial index is removed
-- inside this migration transaction so duplicate legacy rows can be labelled
-- before they are cancelled and the invariant is recreated.
DROP INDEX IF EXISTS agent_interactions_open_fingerprint;
UPDATE agent_interactions
SET decision_key = 'legacy:' || lower(regexp_replace(title, '[^a-zA-Z0-9]+', '-', 'g')),
    fingerprint = md5(lower(regexp_replace(title, '[^a-zA-Z0-9]+', '-', 'g')) || ':' || schema::text)
WHERE decision_key = '' OR fingerprint = '';

WITH ranked AS (
  SELECT id, row_number() OVER (PARTITION BY task_id, agent_id, fingerprint ORDER BY created_at DESC) AS position
  FROM agent_interactions WHERE status = 'open'
)
UPDATE agent_interactions i SET status = 'cancelled'
FROM ranked r WHERE i.id = r.id AND r.position > 1;

UPDATE agent_interactions i SET status = 'cancelled'
WHERE i.status = 'open' AND EXISTS (
  SELECT 1 FROM task_decisions d
  WHERE d.task_id = i.task_id AND d.agent_id = i.agent_id
    AND d.decision_key = i.decision_key AND d.superseded_at IS NULL
);

CREATE UNIQUE INDEX agent_interactions_open_fingerprint ON agent_interactions(task_id, agent_id, fingerprint) WHERE status='open' AND fingerprint <> '';
