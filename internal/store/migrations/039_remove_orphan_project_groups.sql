-- Project groups only exist while at least one project uses them. Earlier
-- versions cleaned these up on project edits but not after a project delete,
-- which can leave stale groups in existing installations.
DELETE FROM project_groups g
WHERE NOT EXISTS (
  SELECT 1 FROM project_group_members m WHERE m.group_id = g.id
);
