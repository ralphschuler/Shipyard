-- Harden installations that already applied the initial memory MVP.
ALTER TABLE memory_facts
  ADD COLUMN IF NOT EXISTS confirmed_by UUID,
  ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ;

DROP INDEX IF EXISTS memory_conversations_message;
CREATE UNIQUE INDEX IF NOT EXISTS memory_conversations_message
  ON memory_conversations(tenant_id,user_id,project_id,task_id,agent_id,message_id);

-- Version history is append-only. Mutable state belongs to memory_facts and
-- its current_version_id pointer, never to a historical version row.
CREATE OR REPLACE FUNCTION memory_fact_versions_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'memory fact versions are append-only';
END $$;
DROP TRIGGER IF EXISTS memory_fact_versions_append_only ON memory_fact_versions;
CREATE TRIGGER memory_fact_versions_append_only BEFORE UPDATE OR DELETE ON memory_fact_versions
  FOR EACH ROW EXECUTE FUNCTION memory_fact_versions_append_only();
