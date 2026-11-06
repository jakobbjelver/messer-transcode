-- Live progress, written by whichever process is holding the file.
-- Kept on the row rather than in the event log: the UI polls it every second
-- and an encode reports every few seconds, so events would be pure churn.

ALTER TABLE files ADD COLUMN progress REAL NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN speed REAL NOT NULL DEFAULT 0;
