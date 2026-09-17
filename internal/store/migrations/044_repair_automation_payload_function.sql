-- Migration 042 was already recorded on some installations before the
-- canonical payload helper was included. Recreate it idempotently so those
-- databases can execute fingerprint-based claims as well.
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
