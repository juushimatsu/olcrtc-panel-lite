-- Persist the first-run auto-setup marker. Existing installations keep their
-- current state because INSERT OR IGNORE does not overwrite user settings.
INSERT OR IGNORE INTO settings (key, value, encrypted, updated_at)
VALUES ('first_run_completed', 'false', 0, datetime('now'));
