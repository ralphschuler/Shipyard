-- Historic versions left a completed child run behind a queued batch. Repair
-- only those provably terminal batches before enforcing the active invariant.
WITH summary AS (
  SELECT b.id,
    count(*) FILTER (WHERE r.status IN ('queued','running')) AS active,
    count(*) FILTER (WHERE r.status='failed') AS failed,
    count(*) FILTER (WHERE r.status='cancelled') AS cancelled,
    count(*) FILTER (WHERE r.status='succeeded') AS succeeded
  FROM agent_run_batches b JOIN agent_runs r ON r.batch_id=b.id
  WHERE b.status IN ('queued','running')
  GROUP BY b.id
)
UPDATE agent_run_batches b SET
  status=CASE WHEN s.failed>0 AND (s.succeeded>0 OR s.cancelled>0) THEN 'partial'
              WHEN s.failed>0 THEN 'failed'
              WHEN s.cancelled>0 AND s.succeeded>0 THEN 'partial'
              WHEN s.cancelled>0 THEN 'cancelled'
              ELSE 'succeeded' END,
  updated_at=now()
FROM summary s WHERE b.id=s.id AND s.active=0;

-- A manual retry is intentional only after the previous task/agent batch has
-- completed. Multiple repository targets still belong to the same batch.
CREATE UNIQUE INDEX IF NOT EXISTS agent_run_batches_one_active_manual
ON agent_run_batches(task_id, agent_id)
WHERE rule_id IS NULL AND status IN ('queued','running');
