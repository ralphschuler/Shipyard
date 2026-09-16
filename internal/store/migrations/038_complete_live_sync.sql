-- New UI projections also depend on records introduced after the first
-- LISTEN/NOTIFY migration.  Keep this explicit rather than falling back to a
-- page reload: each trigger carries the changed table to the SSE client.
CREATE TRIGGER taskboard_live_task_transitions AFTER INSERT OR UPDATE OR DELETE ON task_transitions FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_skill_sources AFTER INSERT OR UPDATE OR DELETE ON skill_sources FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_skills AFTER INSERT OR UPDATE OR DELETE ON skills FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_installed_skills AFTER INSERT OR UPDATE OR DELETE ON installed_skills FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_api_tokens AFTER INSERT OR UPDATE OR DELETE ON api_tokens FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_audit_events AFTER INSERT OR UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_project_sources AFTER INSERT OR UPDATE OR DELETE ON project_sources FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_integration_deliveries AFTER INSERT OR UPDATE OR DELETE ON integration_deliveries FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_workspace_preferences AFTER INSERT OR UPDATE OR DELETE ON workspace_preferences FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_agent_prompt_policy AFTER INSERT OR UPDATE OR DELETE ON agent_prompt_policy FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
