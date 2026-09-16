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
