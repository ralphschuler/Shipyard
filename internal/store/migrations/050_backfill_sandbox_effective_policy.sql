-- Preserve compatibility for runs created before effective sandbox policies
-- were snapshotted. Only the legacy empty value is backfilled; existing run
-- snapshots remain immutable.
UPDATE agent_runs AS r
SET sandbox_effective = jsonb_build_object(
  'Name', p.name,
  'Description', p.description,
  'Mounts', p.mounts,
  'NetworkMode', p.network_mode,
  'WriteMode', p.write_mode,
  'Active', p.active
)
FROM sandbox_profiles AS p
WHERE p.name = r.sandbox_profile
  AND r.sandbox_effective = '{}'::jsonb;
