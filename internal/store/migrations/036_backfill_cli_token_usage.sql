-- Codex prints an authoritative total at the end of a CLI run. Earlier
-- versions stored terminal bytes divided by four, which is not a token count
-- and becomes wildly inaccurate for dependency output or large diffs.
WITH latest_cli_summary AS (
  SELECT DISTINCT ON (agent_run_id)
    agent_run_id,
    regexp_replace(
      (regexp_match(message, '(?i)tokens[[:space:]]+used[[:space:]]*[:[:space:]]+([0-9][0-9,._[:space:]]*)'))[1],
      '[^0-9]', '', 'g'
    )::integer AS token_usage
  FROM agent_run_logs
  WHERE message ~* 'tokens[[:space:]]+used'
  ORDER BY agent_run_id, sequence DESC
)
UPDATE agent_runs AS run
SET token_usage = summary.token_usage
FROM latest_cli_summary AS summary
WHERE run.id = summary.agent_run_id
  AND summary.token_usage > 0;
