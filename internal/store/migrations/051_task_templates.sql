CREATE TABLE IF NOT EXISTS task_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('bug', 'feature', 'review')),
    description TEXT NOT NULL DEFAULT '',
    required_fields JSONB NOT NULL DEFAULT '[]'::jsonb,
    default_priority TEXT NOT NULL DEFAULT 'normal',
    default_label_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    version INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS template_id UUID;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS template_version INTEGER;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS template_snapshot JSONB;
CREATE INDEX IF NOT EXISTS task_templates_board_idx ON task_templates(board_id, enabled);
