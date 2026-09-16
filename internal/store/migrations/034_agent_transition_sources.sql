-- Agent-requested routes and interaction pauses are validated workflow moves.
-- They must be auditable as their own sources instead of failing the former
-- browser/MCP-only constraint after an otherwise successful agent run.
ALTER TABLE task_transitions DROP CONSTRAINT IF EXISTS task_transitions_source_check;
ALTER TABLE task_transitions
  ADD CONSTRAINT task_transitions_source_check
  CHECK(source IN ('web','mcp','automation','agent_failure','agent_review','agent_interaction'));
