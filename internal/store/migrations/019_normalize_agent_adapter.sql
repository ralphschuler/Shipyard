UPDATE agents SET adapter='codex' WHERE adapter IN ('', 'codex_local');
ALTER TABLE agents ALTER COLUMN adapter SET DEFAULT 'codex';
