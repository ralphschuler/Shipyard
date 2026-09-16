CREATE TABLE project_groups (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL CHECK(length(trim(name))>0), description TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT '#3158d4', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX project_groups_name_unique ON project_groups(lower(name));
CREATE TABLE project_group_members (
  group_id UUID NOT NULL REFERENCES project_groups(id) ON DELETE CASCADE, project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(group_id,project_id)
);
CREATE TABLE task_target_projects (task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE, PRIMARY KEY(task_id,project_id));
CREATE TABLE task_target_groups (task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, group_id UUID NOT NULL REFERENCES project_groups(id) ON DELETE CASCADE, PRIMARY KEY(task_id,group_id));
ALTER TABLE agent_runs ADD COLUMN target_project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX agent_runs_target_project ON agent_runs(target_project_id);
CREATE TRIGGER taskboard_live_project_groups AFTER INSERT OR UPDATE OR DELETE ON project_groups FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_project_group_members AFTER INSERT OR UPDATE OR DELETE ON project_group_members FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_task_target_projects AFTER INSERT OR UPDATE OR DELETE ON task_target_projects FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_task_target_groups AFTER INSERT OR UPDATE OR DELETE ON task_target_groups FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
