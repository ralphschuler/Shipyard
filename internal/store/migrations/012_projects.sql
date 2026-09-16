CREATE TABLE projects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  repository_url TEXT NOT NULL DEFAULT '',
  default_branch TEXT NOT NULL DEFAULT 'main',
  local_path TEXT NOT NULL DEFAULT '',
  last_synced_at TIMESTAMPTZ,
  last_sync_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX projects_name_unique ON projects (lower(name));
CREATE TABLE board_projects (
  board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (board_id, project_id)
);
CREATE TRIGGER taskboard_live_projects AFTER INSERT OR UPDATE OR DELETE ON projects FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
CREATE TRIGGER taskboard_live_board_projects AFTER INSERT OR UPDATE OR DELETE ON board_projects FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
