CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  username      TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'admin',
  feed_token    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);
CREATE UNIQUE INDEX users_username ON users(username) WHERE username <> '';

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup_complete). Everything a person chooses
-- is in user_settings under their id.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

-- A place outside the box: a cloud account or a server. kind = gdrive |
-- onedrive | dropbox | b2 | s3 | sftp | webdav | pocketfiles | rclone.
-- config = non-secret JSON (host, user, bucket, endpoint, root folder…);
-- secret = sealed JSON (keys, passwords, the OAuth token). pocketfiles
-- connections act for acting_user through Pocket Core and hold no secret.
CREATE TABLE connections (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  config      TEXT NOT NULL DEFAULT '{}',
  secret      TEXT NOT NULL DEFAULT '',
  acting_user INTEGER REFERENCES users(id) ON DELETE SET NULL,
  status      TEXT NOT NULL DEFAULT '',
  last_error  TEXT NOT NULL DEFAULT '',
  checked_at  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- A restic repository. location = local (path on the box) | connection
-- (path inside a connection, through rclone) | usb (a removable disk,
-- mounted for each use) | uri (any restic repository string).
-- The password is sealed, or read from password_file on the box.
-- managed_by = pocket (this app runs its checks and prunes) | backrest.
CREATE TABLE repositories (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL,
  location       TEXT NOT NULL,
  path           TEXT NOT NULL DEFAULT '',
  connection_id  INTEGER REFERENCES connections(id) ON DELETE RESTRICT,
  password       TEXT NOT NULL DEFAULT '',
  password_file  TEXT NOT NULL DEFAULT '',
  options        TEXT NOT NULL DEFAULT '{}',
  managed_by     TEXT NOT NULL DEFAULT 'pocket',
  backrest_repo  TEXT NOT NULL DEFAULT '',
  check_schedule TEXT NOT NULL DEFAULT '',
  check_read_pct INTEGER NOT NULL DEFAULT 0,
  prune_schedule TEXT NOT NULL DEFAULT '',
  copies_paused  INTEGER NOT NULL DEFAULT 0,
  paused_reason  TEXT NOT NULL DEFAULT '',
  stats          TEXT NOT NULL DEFAULT '{}',
  status         TEXT NOT NULL DEFAULT '',
  last_error     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Something to protect. kind = paths | sqlite | postgres | mariadb | volume |
-- container_files | container_command | host_command. config = JSON per kind;
-- dumps and exports land in the job's staging folder under output.
-- project = the compose project it covers (the coverage check).
CREATE TABLE sources (
  id               INTEGER PRIMARY KEY,
  name             TEXT NOT NULL,
  kind             TEXT NOT NULL,
  enabled          INTEGER NOT NULL DEFAULT 1,
  config           TEXT NOT NULL DEFAULT '{}',
  secret           TEXT NOT NULL DEFAULT '',
  project          TEXT NOT NULL DEFAULT '',
  notes            TEXT NOT NULL DEFAULT '',
  sort_order       REAL NOT NULL DEFAULT 0,
  last_ok_at       TEXT NOT NULL DEFAULT '',
  last_error       TEXT NOT NULL DEFAULT '',
  last_size        INTEGER NOT NULL DEFAULT 0,
  last_duration_ms INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- A backup job. kind = backup (snapshot into a repository) | pull (copy a
-- cloud folder home). engine = pocket (this app snapshots on its schedule)
-- | backrest (a Backrest plan snapshots; its hooks call this app before and
-- after). config = JSON: retention, staging folder, extra paths/excludes,
-- heartbeat, notifications, pull settings, required mounts.
CREATE TABLE jobs (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'backup',
  engine        TEXT NOT NULL DEFAULT 'pocket',
  backrest_plan TEXT NOT NULL DEFAULT '',
  repository_id INTEGER REFERENCES repositories(id) ON DELETE SET NULL,
  schedule      TEXT NOT NULL DEFAULT '',
  enabled       INTEGER NOT NULL DEFAULT 1,
  config        TEXT NOT NULL DEFAULT '{}',
  sort_order    REAL NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE job_sources (
  job_id    INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  PRIMARY KEY (job_id, source_id)
);

-- After a job's snapshot the repository is copied elsewhere. mode = mirror
-- (an exact copy of the repository's files: a folder on the box, a
-- connection) | restic (restic copy of the new snapshots into another
-- repository, which keeps its own history).
CREATE TABLE copies (
  id                   INTEGER PRIMARY KEY,
  job_id               INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  name                 TEXT NOT NULL,
  mode                 TEXT NOT NULL DEFAULT 'mirror',
  connection_id        INTEGER REFERENCES connections(id) ON DELETE RESTRICT,
  path                 TEXT NOT NULL DEFAULT '',
  target_repository_id INTEGER REFERENCES repositories(id) ON DELETE CASCADE,
  enabled              INTEGER NOT NULL DEFAULT 1,
  options              TEXT NOT NULL DEFAULT '{}',
  sort_order           REAL NOT NULL DEFAULT 0,
  last_ok_at           TEXT NOT NULL DEFAULT '',
  last_error           TEXT NOT NULL DEFAULT '',
  last_duration_ms     INTEGER NOT NULL DEFAULT 0,
  created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX copies_job ON copies(job_id);

-- One piece of work and how it went. kind = backup | prep | post | check |
-- prune | copy | restore | pull | source | hook. steps = JSON list of
-- {name, status, ms, detail}; log = the output, capped.
CREATE TABLE runs (
  id            INTEGER PRIMARY KEY,
  job_id        INTEGER REFERENCES jobs(id) ON DELETE SET NULL,
  repository_id INTEGER REFERENCES repositories(id) ON DELETE SET NULL,
  kind          TEXT NOT NULL,
  trigger       TEXT NOT NULL DEFAULT 'manual',
  status        TEXT NOT NULL DEFAULT 'running',
  title         TEXT NOT NULL DEFAULT '',
  started_at    TEXT NOT NULL,
  finished_at   TEXT NOT NULL DEFAULT '',
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  snapshot_id   TEXT NOT NULL DEFAULT '',
  bytes_added   INTEGER NOT NULL DEFAULT 0,
  bytes_total   INTEGER NOT NULL DEFAULT 0,
  files_new     INTEGER NOT NULL DEFAULT 0,
  files_changed INTEGER NOT NULL DEFAULT 0,
  summary       TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT '',
  steps         TEXT NOT NULL DEFAULT '[]',
  log           TEXT NOT NULL DEFAULT '',
  user_id       INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX runs_job ON runs(job_id, started_at);
CREATE INDEX runs_started ON runs(started_at);
CREATE INDEX runs_running ON runs(status) WHERE status = 'running';

-- Compose projects (or lone containers) deliberately left out of backups.
CREATE TABLE coverage_skips (
  project    TEXT PRIMARY KEY,
  reason     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Backrest operations already reported (warnings and errors are told once).
CREATE TABLE backrest_seen (
  op_id   TEXT PRIMARY KEY,
  seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
