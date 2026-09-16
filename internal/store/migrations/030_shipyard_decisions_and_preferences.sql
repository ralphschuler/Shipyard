-- A decision is durable task state, rather than an incidental comment in a run.
CREATE TABLE task_decisions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
  interaction_id UUID REFERENCES agent_interactions(id) ON DELETE SET NULL,
  decision_key TEXT NOT NULL,
  title TEXT NOT NULL,
  response JSONB NOT NULL,
  freeform_answer TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  superseded_at TIMESTAMPTZ,
  reopen_reason TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX task_decisions_active_key ON task_decisions(task_id, agent_id, decision_key) WHERE superseded_at IS NULL;

ALTER TABLE agent_interactions ADD COLUMN decision_key TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_interactions ADD COLUMN fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_interactions ADD COLUMN continuation_run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX agent_interactions_open_fingerprint ON agent_interactions(task_id, agent_id, fingerprint) WHERE status='open' AND fingerprint <> '';
CREATE UNIQUE INDEX agent_interactions_continuation_once ON agent_interactions(continuation_run_id) WHERE continuation_run_id IS NOT NULL;

ALTER TABLE agents ADD COLUMN prompt_prefix TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN prompt_suffix TEXT NOT NULL DEFAULT '';

CREATE TABLE workspace_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  theme TEXT NOT NULL DEFAULT 'system' CHECK(theme IN ('system','light','dark')),
  shortcut_hints BOOLEAN NOT NULL DEFAULT true,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE agent_prompt_policy (
  id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id),
  prompt_prefix TEXT NOT NULL DEFAULT '',
  prompt_suffix TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO agent_prompt_policy(id) VALUES(true) ON CONFLICT DO NOTHING;

-- Preserve older answers so an existing task does not re-ask the same question.
INSERT INTO task_decisions(task_id,agent_id,interaction_id,decision_key,title,response,resolved_by,resolved_at)
SELECT task_id,agent_id,id,'legacy:' || lower(regexp_replace(title, '[^a-zA-Z0-9]+', '-', 'g')),title,response,answered_by,COALESCE(answered_at,created_at)
FROM agent_interactions WHERE status='answered' AND response IS NOT NULL
ON CONFLICT DO NOTHING;

CREATE TRIGGER taskboard_live_decisions AFTER INSERT OR UPDATE OR DELETE ON task_decisions FOR EACH ROW EXECUTE FUNCTION taskboard_notify_change();
