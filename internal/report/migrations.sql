CREATE TABLE IF NOT EXISTS backup_jobs (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  category text,
  subcategory text,
  result_status text,
  asset_id text,
  asset_name text,
  policy_name text,
  started_at timestamptz,
  completed_at timestamptz,
  bytes_transferred bigint,
  created_at timestamptz,
  captured_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_backup_jobs_tenant_created ON backup_jobs (tenant, created_at);
CREATE INDEX IF NOT EXISTS idx_backup_jobs_server_created ON backup_jobs (server, created_at);

CREATE TABLE IF NOT EXISTS copies (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  asset_id text,
  policy_name text,
  copy_type text,
  create_time timestamptz,
  expiration_time timestamptz,
  retention_time timestamptz,
  retention_lock boolean,
  storage_system_id text,
  location text,
  size_bytes bigint,
  captured_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_copies_server_create ON copies (server, create_time);

CREATE TABLE IF NOT EXISTS assets (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  name text,
  type text,
  protection_status text,
  last_available_copy_time timestamptz,
  policy_name text,
  updated_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS protection_policies (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  name text,
  objectives jsonb,
  updated_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS capture_runs (
  id bigserial PRIMARY KEY,
  server text NOT NULL,
  started_at timestamptz NOT NULL,
  finished_at timestamptz,
  ok boolean NOT NULL DEFAULT false,
  error text,
  counts jsonb,
  tool_version text
);
