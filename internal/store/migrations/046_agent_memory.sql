-- Persistent, tenant-scoped agent memory.  Version rows are append-only;
-- current/active is explicit so the uniqueness rule is unambiguous.
CREATE TABLE memory_conversations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL, user_id UUID NOT NULL,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  thread_id TEXT NOT NULL, message_id TEXT NOT NULL, run_id UUID NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('system','user','assistant','tool')),
  content TEXT NOT NULL, content_hash TEXT NOT NULL, search_vector TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
  occurred_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ, provenance_json JSONB NOT NULL
);
CREATE INDEX memory_conversations_page ON memory_conversations(tenant_id,user_id,project_id,task_id,agent_id,thread_id,occurred_at DESC,id DESC);
CREATE INDEX memory_conversations_search ON memory_conversations USING GIN(search_vector);
CREATE UNIQUE INDEX memory_conversations_message ON memory_conversations(tenant_id,task_id,message_id);

CREATE TABLE memory_facts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL, user_id UUID NOT NULL,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  subject TEXT NOT NULL, predicate TEXT NOT NULL, object_json JSONB NOT NULL, dedupe_key TEXT NOT NULL,
  confidence NUMERIC(5,4) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
  status TEXT NOT NULL CHECK (status IN ('pending','confirmed','revoked','superseded')) DEFAULT 'pending',
  high_impact BOOLEAN NOT NULL DEFAULT false, current_version_id UUID, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ,
  UNIQUE(tenant_id,user_id,project_id,task_id,agent_id,dedupe_key)
);
CREATE TABLE memory_fact_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), fact_id UUID NOT NULL REFERENCES memory_facts(id) ON DELETE CASCADE,
  version_no INTEGER NOT NULL CHECK (version_no > 0), supersedes_version_id UUID REFERENCES memory_fact_versions(id),
  value_json JSONB NOT NULL, valid_from TIMESTAMPTZ NOT NULL, valid_until TIMESTAMPTZ,
  confidence NUMERIC(5,4) NOT NULL CHECK (confidence >= 0 AND confidence <= 1), active BOOLEAN NOT NULL DEFAULT true,
  change_reason TEXT NOT NULL, message_id TEXT NOT NULL, run_id UUID NOT NULL, created_by UUID NOT NULL,
  confirmed_by UUID, confirmed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), provenance_json JSONB NOT NULL,
  UNIQUE(fact_id,version_no)
);
CREATE UNIQUE INDEX memory_one_active_version ON memory_fact_versions(fact_id) WHERE active;
ALTER TABLE memory_facts ADD CONSTRAINT memory_current_version_fk FOREIGN KEY(current_version_id) REFERENCES memory_fact_versions(id);

CREATE TABLE memory_audit_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL, user_id UUID NOT NULL,
  project_id UUID NOT NULL, task_id UUID NOT NULL, agent_id UUID NOT NULL, action TEXT NOT NULL,
  target_id UUID, actor_id UUID NOT NULL, actor_role TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL, metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX memory_audit_scope ON memory_audit_events(tenant_id,user_id,project_id,task_id,agent_id,created_at DESC);
