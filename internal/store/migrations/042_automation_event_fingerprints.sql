-- A transport event ID is not a semantic identity: retries and QA returns can
-- arrive with a new ID. Claims are rule-scoped so one event may still trigger
-- multiple independently configured rules, while the same semantic delivery
-- can only create one batch. The payload is retained for audit/debugging.
CREATE TABLE automation_event_claims (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint TEXT NOT NULL UNIQUE,
    event_id UUID REFERENCES automation_events(id) ON DELETE SET NULL,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES automation_rules(id) ON DELETE CASCADE,
    target_column_id UUID,
    return_generation TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}',
    batch_id UUID REFERENCES agent_run_batches(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed','blocked')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX automation_event_claims_task_rule ON automation_event_claims(task_id, rule_id, created_at DESC);

-- Keep the migration backfill independent from the Go fingerprint encoder.
-- JSON objects are order-insensitive, transport metadata is ignored at every
-- nesting level, and array order remains significant because arrays can carry
-- ordered transitions or target lists.
CREATE OR REPLACE FUNCTION canonical_automation_payload(value JSONB)
RETURNS JSONB
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    result JSONB;
BEGIN
    IF jsonb_typeof(value) = 'object' THEN
        SELECT COALESCE(jsonb_object_agg(key, canonical_automation_payload(child)), '{}'::jsonb)
        INTO result
        FROM jsonb_each(value) AS fields(key, child)
        WHERE lower(trim(key)) NOT IN ('event_id', 'delivery_id', 'transport_id', 'occurred_at', 'received_at');
        RETURN result;
    ELSIF jsonb_typeof(value) = 'array' THEN
        SELECT COALESCE(jsonb_agg(canonical_automation_payload(child) ORDER BY ordinal), '[]'::jsonb)
        INTO result
        FROM jsonb_array_elements(value) WITH ORDINALITY AS items(child, ordinal);
        RETURN result;
    END IF;
    RETURN value;
END;
$$;

-- Runs created before this migration are the authoritative evidence that an
-- automation transition already started. Backfill one durable claim per
-- existing batch. The legacy fingerprint is intentionally namespaced; the
-- store also matches these rows by their semantic fields and normalized
-- payload when a retried transport delivery arrives.
INSERT INTO automation_event_claims(
    fingerprint, event_id, task_id, rule_id, target_column_id,
    return_generation, payload, batch_id, status, attempts, last_error,
    created_at, updated_at
)
SELECT
    'legacy:' || b.id::text,
    b.event_id,
    b.task_id,
    b.rule_id,
    r.target_column_id,
    COALESCE(e.payload->>'return_generation', e.payload->>'qa_return_generation',
        e.payload->>'rollback_generation', e.payload->>'generation', ''),
    e.payload,
    b.id,
    CASE WHEN b.status = 'partial' THEN 'failed' WHEN b.status = 'cancelled' THEN 'blocked' ELSE b.status END,
    GREATEST(COALESCE(e.attempts, 0), 0),
    COALESCE(e.last_error, ''),
    b.created_at,
    b.updated_at
FROM agent_run_batches b
JOIN automation_events e ON e.id = b.event_id
LEFT JOIN automation_rules r ON r.id = b.rule_id
WHERE b.event_id IS NOT NULL
  AND b.rule_id IS NOT NULL
ON CONFLICT (fingerprint) DO NOTHING;

-- Very old installations may have agent_runs from before run batches were
-- introduced. Preserve those starts as claims too; DISTINCT ON collapses a
-- pre-fan-out event/rule pair to one semantic claim.
INSERT INTO automation_event_claims(
    fingerprint, event_id, task_id, rule_id, target_column_id,
    return_generation, payload, status, attempts, last_error,
    created_at, updated_at
)
SELECT DISTINCT ON (r.event_id, r.rule_id)
    'legacy:run:' || r.id::text,
    r.event_id,
    r.task_id,
    r.rule_id,
    ar.target_column_id,
    COALESCE(e.payload->>'return_generation', e.payload->>'qa_return_generation',
        e.payload->>'rollback_generation', e.payload->>'generation', ''),
    e.payload,
    CASE WHEN r.status IN ('queued', 'running', 'succeeded', 'failed', 'blocked') THEN r.status WHEN r.status = 'cancelled' THEN 'blocked' ELSE 'failed' END,
    GREATEST(COALESCE(e.attempts, 0), 0),
    r.error_message,
    r.created_at,
    r.created_at
FROM agent_runs r
JOIN automation_events e ON e.id = r.event_id
LEFT JOIN automation_rules ar ON ar.id = r.rule_id
WHERE r.event_id IS NOT NULL
  AND r.rule_id IS NOT NULL
  AND r.batch_id IS NULL
ORDER BY r.event_id, r.rule_id, r.created_at;
