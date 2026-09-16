-- Worker-originated transitions are first-class workflow events.  The original
-- schema only admitted browser and MCP changes, which made a successful
-- automation unable to advance its task despite a valid transition.
ALTER TABLE task_transitions DROP CONSTRAINT IF EXISTS task_transitions_source_check;
ALTER TABLE task_transitions
  ADD CONSTRAINT task_transitions_source_check
  CHECK(source IN ('web','mcp','automation','agent_failure'));
