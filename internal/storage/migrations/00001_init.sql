-- +goose Up
CREATE TABLE IF NOT EXISTS marquees (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  user TEXT NOT NULL,
  ssh_private_key TEXT NOT NULL DEFAULT '',
  domains_input TEXT,
  https_enabled INTEGER NOT NULL DEFAULT 1,
  tls_certificate_source TEXT,
  acme_email TEXT,
  build_platform TEXT,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS server_metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS props (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  repository_url TEXT NOT NULL,
  private INTEGER NOT NULL DEFAULT 0,
  default_branch TEXT NOT NULL,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  branches_json TEXT NOT NULL DEFAULT '[]',
  last_synced_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS playspecs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  description TEXT,
  base_compose_yaml TEXT NOT NULL,
  services_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS playgrounds (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  playspec_id INTEGER,
  marquee_id INTEGER,
  compose_project TEXT,
  root_domain TEXT,
  routing_scheme TEXT,
  internal_password TEXT,
  env_overrides_json TEXT NOT NULL DEFAULT '{}',
  service_branches_json TEXT NOT NULL DEFAULT '{}',
  generated_compose_yaml TEXT NOT NULL DEFAULT '',
  services_json TEXT NOT NULL DEFAULT '[]',
  service_urls_json TEXT NOT NULL DEFAULT '[]',
  build_statuses_json TEXT NOT NULL DEFAULT '[]',
  creation_steps_json TEXT NOT NULL DEFAULT '[]',
  expires_at TEXT,
  last_applied_at TEXT,
  error_message TEXT,
  state_reason TEXT,
  state_reasons_json TEXT NOT NULL DEFAULT '[]',
  build_warnings_json TEXT NOT NULL DEFAULT '[]',
  error_details_json TEXT NOT NULL DEFAULT '{}',
  playguard_repair_reason TEXT,
  playguard_repair_lock_until TEXT,
  needs_recreation INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(playspec_id) REFERENCES playspecs(id) ON DELETE SET NULL,
  FOREIGN KEY(marquee_id) REFERENCES marquees(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS build_records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  playground_id INTEGER,
  prop_id INTEGER,
  service_name TEXT NOT NULL,
  branch TEXT NOT NULL,
  commit_sha TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  image_ref TEXT NOT NULL DEFAULT '',
  build_dockerfile_path TEXT NOT NULL DEFAULT '',
  build_target TEXT NOT NULL DEFAULT '',
  build_args_digest TEXT NOT NULL DEFAULT '',
  build_identity_digest TEXT NOT NULL DEFAULT '',
  build_platform TEXT NOT NULL DEFAULT '',
  build_cache_key TEXT NOT NULL DEFAULT '',
  reused INTEGER NOT NULL DEFAULT 0,
  logs TEXT NOT NULL DEFAULT '',
  error_message TEXT,
  started_at TEXT,
  completed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS async_operations (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  error_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS async_operations;
DROP TABLE IF EXISTS build_records;
DROP TABLE IF EXISTS playgrounds;
DROP TABLE IF EXISTS playspecs;
DROP TABLE IF EXISTS props;
DROP TABLE IF EXISTS server_metadata;
DROP TABLE IF EXISTS marquees;
