-- Coding runs create isolated patches and must not advance a task until a
-- person has accepted that patch. Read-only review rules may advance directly.
ALTER TABLE automation_rules
  ADD COLUMN require_delivery_approval BOOLEAN NOT NULL DEFAULT true;
