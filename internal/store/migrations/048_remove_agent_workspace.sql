-- Agent profiles describe behavior only. Run workspaces are resolved from
-- task project targets and historical run snapshots remain untouched.
ALTER TABLE agents DROP COLUMN IF EXISTS workspace_path;
