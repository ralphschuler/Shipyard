-- Retention scans all scopes in one operational pass. These indexes keep the
-- scope discovery and age predicates bounded without weakening scope checks.
CREATE INDEX IF NOT EXISTS memory_conversations_retention_scope
  ON memory_conversations(tenant_id,user_id,project_id,task_id,agent_id,occurred_at)
  WHERE expires_at IS NOT NULL OR deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS memory_facts_retention_updated
  ON memory_facts(tenant_id,user_id,project_id,task_id,agent_id,updated_at);
