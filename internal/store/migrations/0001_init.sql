-- 0001_init.sql — the state schema.
--
-- Every transition is committed before the next step runs, so a crash resumes
-- rather than restarts. Anything the planner needs lives on `files`; anything
-- that happened lives in `events`.

CREATE TABLE files (
  id             INTEGER PRIMARY KEY,
  path           TEXT    NOT NULL UNIQUE,
  library        TEXT    NOT NULL,
  size_bytes     INTEGER NOT NULL,
  mtime_ns       INTEGER NOT NULL,
  -- sha256(size, mtime) at probe time; a mismatch invalidates the probe
  probe_key      TEXT,
  container      TEXT,
  video_codec    TEXT,
  width          INTEGER,
  height         INTEGER,
  bitrate_kbps   INTEGER,
  duration_s     REAL,
  pix_fmt        TEXT,
  audio_tracks   TEXT,
  sub_tracks     TEXT,
  tmdb_id        INTEGER,
  decision       TEXT,
  reason         TEXT,
  reason_code    TEXT,
  target_kbps    INTEGER,
  state          TEXT    NOT NULL DEFAULT 'new',
  attempts       INTEGER NOT NULL DEFAULT 0,
  last_error     TEXT,
  last_error_code TEXT,
  node_id        TEXT,
  claimed_at     INTEGER,
  lease_until    INTEGER,
  started_at     INTEGER,
  finished_at    INTEGER,
  bytes_before   INTEGER,
  bytes_after    INTEGER,
  vmaf           REAL,
  updated_at     INTEGER NOT NULL
);

CREATE INDEX idx_files_state    ON files(state);
CREATE INDEX idx_files_decision ON files(decision);
CREATE INDEX idx_files_library  ON files(library);

CREATE TABLE jobs (
  id         TEXT PRIMARY KEY,
  file_id    INTEGER REFERENCES files(id),
  type       TEXT NOT NULL,
  payload    TEXT,
  state      TEXT NOT NULL,
  node_id    TEXT,
  created_at INTEGER,
  started_at INTEGER,
  finished_at INTEGER,
  result     TEXT,
  error_code TEXT
);

CREATE INDEX idx_jobs_state ON jobs(state);

CREATE TABLE nodes (
  id        TEXT PRIMARY KEY,
  hostname  TEXT,
  os        TEXT,
  arch      TEXT,
  version   TEXT,
  encoders  TEXT,
  paths     TEXT,
  free_gb   REAL,
  last_seen INTEGER
);

CREATE TABLE events (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  at      INTEGER NOT NULL,
  level   TEXT    NOT NULL,
  code    TEXT,
  file_id INTEGER,
  job_id  TEXT,
  message TEXT    NOT NULL,
  data    TEXT
);

CREATE INDEX idx_events_at ON events(at);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
