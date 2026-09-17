-- Terminal output can be high-volume.  Scope its notification to the owning
-- run so clients with another run open do not fetch an empty log delta.
CREATE OR REPLACE FUNCTION taskboard_notify_change() RETURNS trigger AS $$
DECLARE
  entity_id TEXT;
  run_id TEXT := '';
BEGIN
  IF TG_OP = 'DELETE' THEN
    entity_id := COALESCE(to_jsonb(OLD)->>'id', '');
    IF TG_TABLE_NAME = 'agent_run_logs' THEN
      run_id := COALESCE(to_jsonb(OLD)->>'agent_run_id', '');
    END IF;
  ELSE
    entity_id := COALESCE(to_jsonb(NEW)->>'id', '');
    IF TG_TABLE_NAME = 'agent_run_logs' THEN
      run_id := COALESCE(to_jsonb(NEW)->>'agent_run_id', '');
    END IF;
  END IF;
  PERFORM pg_notify('taskboard_events', json_build_object(
    'table', TG_TABLE_NAME,
    'action', TG_OP,
    'id', entity_id,
    'run_id', run_id
  )::text);
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
