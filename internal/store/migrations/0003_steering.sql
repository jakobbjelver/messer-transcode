-- 0003_steering.sql — what the app can now do, not just show.
--
-- Three things the read-only release could not express:
--   * an operator's opinion about order (priority) and about a file's fate
--     (retry/skip) — the queue used to be the planner's alone;
--   * the run a file was claimed in. Until now a "run" was recomputed from
--     whatever schedule happened to be configured when somebody looked, so
--     editing the schedule rewrote the past;
--   * the configuration's own history. The image is distroless (no git) and the
--     live config is a bind mount, so revisions live here, beside the queue.

CREATE TABLE runs (
  id         INTEGER PRIMARY KEY,          -- unix start, UTC seconds; also the URL id
  start_at   INTEGER NOT NULL,
  end_at     INTEGER NOT NULL DEFAULT 0,   -- 0 while a manual run is still open
  spec       TEXT    NOT NULL,             -- the window that produced it, frozen
  kind       TEXT    NOT NULL,             -- weekday | weekend | manual
  closed_at  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE config_revisions (
  rev        INTEGER PRIMARY KEY AUTOINCREMENT,
  sha256     TEXT    NOT NULL,             -- over canonical JSON, not raw bytes
  created_at INTEGER NOT NULL,
  actor      TEXT    NOT NULL,             -- web | cli | adopt | revert | migration
  message    TEXT    NOT NULL,
  parent_rev INTEGER,
  content    TEXT    NOT NULL
);

ALTER TABLE files ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN run_id   INTEGER REFERENCES runs(id);

-- The queue sorts by priority, the done view sorts by finish, and the runs
-- ledger groups by run. All three are paginated over ~3k rows.
CREATE INDEX idx_files_priority ON files(priority);
CREATE INDEX idx_files_run      ON files(run_id);
CREATE INDEX idx_files_finished ON files(finished_at);
