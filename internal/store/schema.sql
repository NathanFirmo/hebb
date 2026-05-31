PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS traces (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  scope TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  valence REAL NOT NULL DEFAULT 0.0,
  confidence REAL NOT NULL DEFAULT 0.7,
  strength REAL NOT NULL DEFAULT 0.5,
  salience REAL NOT NULL DEFAULT 0.5,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  last_recalled_at TEXT,
  recall_count INTEGER NOT NULL DEFAULT 0,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  embedding_pending INTEGER NOT NULL DEFAULT 1,
  CHECK (kind IN ('fact', 'observation', 'decision', 'preference', 'procedure', 'event', 'episode', 'semantic_summary', 'warning', 'question')),
  CHECK (status IN ('active', 'inhibited', 'consolidated', 'archived', 'forgotten'))
);

CREATE VIRTUAL TABLE IF NOT EXISTS trace_fts USING fts5(
  title,
  body,
  kind,
  scope,
  content='traces',
  content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS traces_ai AFTER INSERT ON traces BEGIN
  INSERT INTO trace_fts(rowid, title, body, kind, scope)
  VALUES (new.id, new.title, new.body, new.kind, new.scope);
END;

CREATE TRIGGER IF NOT EXISTS traces_ad AFTER DELETE ON traces BEGIN
  INSERT INTO trace_fts(trace_fts, rowid, title, body, kind, scope)
  VALUES ('delete', old.id, old.title, old.body, old.kind, old.scope);
END;

CREATE TRIGGER IF NOT EXISTS traces_au AFTER UPDATE ON traces BEGIN
  INSERT INTO trace_fts(trace_fts, rowid, title, body, kind, scope)
  VALUES ('delete', old.id, old.title, old.body, old.kind, old.scope);
  INSERT INTO trace_fts(rowid, title, body, kind, scope)
  VALUES (new.id, new.title, new.body, new.kind, new.scope);
END;

CREATE TABLE IF NOT EXISTS entities (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL DEFAULT '',
  canonical_name TEXT NOT NULL,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  UNIQUE (canonical_name, type)
);

CREATE TABLE IF NOT EXISTS trace_entities (
  trace_id INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  entity_id INTEGER NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0.7,
  PRIMARY KEY (trace_id, entity_id, role)
);

CREATE TABLE IF NOT EXISTS associations (
  id INTEGER PRIMARY KEY,
  from_trace_id INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  to_trace_id INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  strength REAL NOT NULL DEFAULT 0.5,
  confidence REAL NOT NULL DEFAULT 0.7,
  evidence_count INTEGER NOT NULL DEFAULT 1,
  last_reinforced_at TEXT,
  CHECK (from_trace_id <> to_trace_id),
  UNIQUE (from_trace_id, to_trace_id, relation)
);

CREATE TABLE IF NOT EXISTS episodes (
  id INTEGER PRIMARY KEY,
  scope TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  ended_at TEXT,
  summary TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS trace_events (
  id INTEGER PRIMARY KEY,
  trace_id INTEGER NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

