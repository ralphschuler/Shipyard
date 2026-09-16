ALTER TABLE workspace_preferences
  ADD COLUMN language TEXT NOT NULL DEFAULT 'de';

ALTER TABLE workspace_preferences
  ADD CONSTRAINT workspace_preferences_language_check CHECK (language IN ('de', 'en'));
