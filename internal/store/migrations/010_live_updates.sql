CREATE OR REPLACE FUNCTION taskboard_notify_change() RETURNS trigger AS $$
DECLARE
  entity_id TEXT;
BEGIN
  IF TG_OP = 'DELETE' THEN
    entity_id := COALESCE(to_jsonb(OLD)->>'id', '');
  ELSE
    entity_id := COALESCE(to_jsonb(NEW)->>'id', '');
  END IF;
  PERFORM pg_notify('taskboard_events', json_build_object(
    'table', TG_TABLE_NAME,
    'action', TG_OP,
    'id', entity_id
  )::text);
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER taskboard_live_boards AFTER INSERT OR UPDATE OR DELETE ON boards FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_columns AFTER INSERT OR UPDATE OR DELETE ON workflow_columns FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_transitions AFTER INSERT OR UPDATE OR DELETE ON transitions FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_tasks AFTER INSERT OR UPDATE OR DELETE ON tasks FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_comments AFTER INSERT OR UPDATE OR DELETE ON task_comments FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_labels AFTER INSERT OR UPDATE OR DELETE ON labels FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_task_labels AFTER INSERT OR UPDATE OR DELETE ON task_labels FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_agents AFTER INSERT OR UPDATE OR DELETE ON agents FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_agent_skills AFTER INSERT OR UPDATE OR DELETE ON agent_skills FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_rules AFTER INSERT OR UPDATE OR DELETE ON automation_rules FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_events AFTER INSERT OR UPDATE OR DELETE ON automation_events FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_runs AFTER INSERT OR UPDATE OR DELETE ON agent_runs FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_run_logs AFTER INSERT OR UPDATE OR DELETE ON agent_run_logs FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_providers AFTER INSERT OR UPDATE OR DELETE ON provider_settings FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_notifications AFTER INSERT OR UPDATE OR DELETE ON notifications FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_webhooks AFTER INSERT OR UPDATE OR DELETE ON webhook_subscriptions FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
