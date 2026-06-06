-- PPDM ids are issued per-server, not globally unique, so the primary key is (id, server).
CREATE TABLE IF NOT EXISTS backup_jobs (
  id text NOT NULL,
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
  captured_at timestamptz NOT NULL,
  PRIMARY KEY (id, server)
);
CREATE INDEX IF NOT EXISTS idx_backup_jobs_tenant_created ON backup_jobs (tenant, created_at);
CREATE INDEX IF NOT EXISTS idx_backup_jobs_server_created ON backup_jobs (server, created_at);

CREATE TABLE IF NOT EXISTS copies (
  id text NOT NULL,
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
  captured_at timestamptz NOT NULL,
  PRIMARY KEY (id, server)
);
CREATE INDEX IF NOT EXISTS idx_copies_server_create ON copies (server, create_time);

CREATE TABLE IF NOT EXISTS assets (
  id text NOT NULL,
  tenant text NOT NULL,
  server text NOT NULL,
  name text,
  type text,
  protection_status text,
  last_available_copy_time timestamptz,
  policy_name text,
  updated_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL,
  PRIMARY KEY (id, server)
);

CREATE TABLE IF NOT EXISTS protection_policies (
  id text NOT NULL,
  tenant text NOT NULL,
  server text NOT NULL,
  name text,
  objectives jsonb,
  updated_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL,
  PRIMARY KEY (id, server)
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

-- Phase 2 (SLA compliance). sla_targets holds the resolved per-asset SLA targets — the only
-- materialized state; verdicts stay on-demand in the compliance view. Keyed by
-- (tenant, asset_type, policy_name) with '' wildcards so an asset matches the most specific
-- row: a per-tenant default ('',''), a policy-derived target ('', policy), or an asset-type
-- override. source records which layer won (policy | override | default).
CREATE TABLE IF NOT EXISTS sla_targets (
  tenant text NOT NULL,
  asset_type text NOT NULL DEFAULT '',
  policy_name text NOT NULL DEFAULT '',
  rpo_seconds bigint NOT NULL,
  retention_days int NOT NULL,
  min_copies int NOT NULL,
  grace_seconds bigint NOT NULL,
  source text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant, asset_type, policy_name)
);

-- compliance evaluates each asset's SLA verdict live (read-only, recomputed per query). It joins
-- the asset to its most specific sla_targets row, then computes three rules using that row's own
-- grace_seconds, so nothing is hard-coded here.
CREATE OR REPLACE VIEW compliance AS
WITH asset_target AS (
  SELECT DISTINCT ON (a.id, a.server)
    a.tenant, a.server, a.id AS asset_id, a.name AS asset_name,
    a.type AS asset_type, a.policy_name, a.last_available_copy_time,
    t.rpo_seconds, t.retention_days, t.min_copies, t.grace_seconds
  FROM assets a
  LEFT JOIN sla_targets t
    ON t.tenant = a.tenant
   AND (t.asset_type = a.type OR t.asset_type = '')
   AND (t.policy_name = a.policy_name OR t.policy_name = '')
  ORDER BY a.id, a.server,
    ((t.asset_type <> '')::int * 2 + (t.policy_name <> '')::int) DESC NULLS LAST
),
evaluated AS (
  SELECT
    at.tenant, at.server, at.asset_id, at.asset_name, at.asset_type, at.policy_name,
    at.rpo_seconds, at.retention_days, at.min_copies,
    (
      EXISTS (
        SELECT 1 FROM backup_jobs j
        WHERE j.asset_id = at.asset_id AND j.server = at.server
          AND j.result_status IN ('SUCCESS', 'OK')
          AND j.created_at >= now() - make_interval(secs => at.rpo_seconds + at.grace_seconds)
      )
      OR (at.last_available_copy_time IS NOT NULL
          AND at.last_available_copy_time >= now() - make_interval(secs => at.rpo_seconds + at.grace_seconds))
    ) AS rpo_ok,
    COALESCE((
      SELECT EXTRACT(EPOCH FROM (c.retention_time - c.create_time)) >= at.retention_days * 86400
      FROM copies c
      WHERE c.asset_id = at.asset_id AND c.server = at.server
        AND c.retention_time IS NOT NULL AND c.create_time IS NOT NULL
      ORDER BY c.create_time DESC
      LIMIT 1
    ), false) AS retention_ok,
    (SELECT count(*) FROM copies c WHERE c.asset_id = at.asset_id AND c.server = at.server) >= at.min_copies AS copies_ok
  FROM asset_target at
)
SELECT
  tenant, server, asset_id, asset_name, asset_type, policy_name,
  rpo_ok, retention_ok, copies_ok,
  (rpo_ok AND retention_ok AND copies_ok) AS compliant,
  array_to_string(ARRAY[
    CASE WHEN NOT rpo_ok THEN 'rpo' END,
    CASE WHEN NOT retention_ok THEN 'retention' END,
    CASE WHEN NOT copies_ok THEN 'copies' END
  ], ',') AS reasons,
  rpo_seconds, retention_days, min_copies
FROM evaluated;

-- copies is correlated by (asset_id, server) from the compliance and rule_321110 views; the
-- only other copies index is on (server, create_time), so add the one those joins actually use.
CREATE INDEX IF NOT EXISTS idx_copies_asset_server ON copies (asset_id, server);

-- Phase 3: 3-2-1-1-0 backup-rule badge, per asset, computed live (read-only) parallel to
-- compliance. media (distinct storage_system_id) and offsite (distinct location) ride
-- provisional copy fields, so the badge is best-effort and labelled as such in the report.
CREATE OR REPLACE VIEW rule_321110 AS
WITH per_asset AS (
  -- One LEFT JOIN pass over copies yields all four copy aggregates (empty-string media/location
  -- excluded from the distinct counts); a no-copies asset keeps a row with zero/false values.
  SELECT a.tenant, a.server, a.id AS asset_id, a.name AS asset_name, a.type AS asset_type,
    count(c.id)                                          AS copies_count,
    count(DISTINCT NULLIF(c.storage_system_id, ''))      AS distinct_media,
    count(DISTINCT NULLIF(c.location, ''))               AS distinct_locations,
    COALESCE(bool_or(c.retention_lock), false)           AS has_immutable,
    NOT EXISTS (SELECT 1 FROM backup_jobs j
       WHERE j.asset_id = a.id AND j.server = a.server AND j.result_status = 'FAILED') AS errors_ok
  FROM assets a
  LEFT JOIN copies c ON c.asset_id = a.id AND c.server = a.server
  GROUP BY a.tenant, a.server, a.id, a.name, a.type
)
SELECT tenant, server, asset_id, asset_name, asset_type,
  copies_count, distinct_media, distinct_locations,
  (copies_count >= 3)      AS copies_ok,
  (distinct_media >= 2)    AS media_ok,
  (distinct_locations >= 2) AS offsite_ok,
  has_immutable            AS immutable_ok,
  errors_ok,
  ((copies_count >= 3) AND (distinct_media >= 2) AND (distinct_locations >= 2)
    AND has_immutable AND errors_ok) AS rule_pass
FROM per_asset;

-- Phase 4a: provenance + dedupe for scheduled report deliveries. One row per (tenant, period
-- occurrence); a failed attempt is overwritten by a later success, and DeliveryExists counts only
-- successes so failures retry until the occurrence's period rolls over.
CREATE TABLE IF NOT EXISTS report_deliveries (
  tenant text NOT NULL,
  period text NOT NULL,
  sent_at timestamptz NOT NULL DEFAULT now(),
  ok boolean NOT NULL,
  error text,
  recipients text,
  PRIMARY KEY (tenant, period)
);
