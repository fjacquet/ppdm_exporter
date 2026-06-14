# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.5.1] - 2026-06-14

### Documentation

- ADRs 0011–0013 record the `cmd/report` backup-assurance reporter decisions that
  previously lived only in `docs/report.md`: 0011 (second binary + PostgreSQL
  persistence + assurance-not-forensic scope), 0012 (per-tenant `/report`
  authorization via sha256 bearer-token registry), 0013 (per-tenant retention with
  default-fallback chain + per-tenant prune).

### Added

- Native `.env` loading at startup (`internal/config.LoadDotEnv`): binary reads
  `.env` from the working directory first, then next to the config file, before
  YAML interpolation runs. Already-set environment variables always take precedence
  (godotenv no-override semantics). Mirrors the docker compose behaviour so
  `cp .env.example .env` works identically for bare-metal and systemd deployments.
  Dependency: `github.com/joho/godotenv v1.5.1`.

## [1.1.0] - 2026-06-06

### Added

- `cmd/report` Phase 4a: scheduled per-tenant report delivery — an in-process scheduler
  (daily/weekly/monthly + hour, UTC) emails each tenant's report (HTML + PDF attachment) over
  SMTP, deduped/audited via `report_deliveries`. Demo adds a Mailpit sink.
- `cmd/report` Phase 4b: per-tenant `/report` access control — `report.tokens` scope each bearer
  token to specific tenants (`authToken` = all-tenants admin); an out-of-scope token gets 403
  before any data access. No new dependencies.
- `cmd/report` Phase 4c: per-tenant retention — a `retention` block (defaultDays + per-tenant
  overrides) prunes `backup_jobs`/`copies` and sets each tenant's first-capture backfill window;
  `defaultDays` falls back to `capture.retentionDays`. Completes Phase 4.

## [1.0.0] - 2026-06-06

### Added
- Scaffold: Go module (`go 1.26.4`), full-contract Makefile, CLI skeleton, Apache-2.0 license.
- Hand-rolled `resty/v2` PPDM client: bearer login (`POST /api/v2/login`), expiry-aware
  re-login + relogin-on-401, generic paginated `GetAll` over the `{page,content}` envelope.
- Snapshot collection model: immutable `Snapshot` + RWMutex store, parallel per-server loop
  with per-collector degradation (`ppdm_up`, `ppdm_collector_up`).
- Four resource collectors: activities (`ppdm_activity_count`, `ppdm_activity_bytes_total`),
  assets (rollups + bounded `ppdm_asset_last_copy_age_seconds`), capacity, health & alerts
  (`ppdm_alert_count{severity,ack_state}`).
- Dual export: unchecked Prometheus collector + OTLP observable-gauge exporter.
- YAML config with `${ENV}` interpolation, `passwordFile`, and SIGHUP + fsnotify hot reload.
- CLI (`--config/--once/--debug`) serving HTTP before the first collection cycle, with `/health`.
- Grafana **Backup Report** dashboard (24h summary: totals, success rate, failures, data
  volume, trend) plus a per-job detail table.
- Opt-in per-job activity metrics (`collection.perJobActivities`): `ppdm_activity_info`,
  `ppdm_activity_job_bytes`, `ppdm_activity_job_duration_seconds` — off by default (cardinality).

- `cmd/report` (backup-assurance reporter, Phase 1): durable PPDM backup-history capture
  (activities/copies/assets/policies) into PostgreSQL — incremental watermark, retention
  prune, `capture_runs` provenance, tenant tagging — plus a Grafana **Backup History**
  view over Postgres. Reuses the shared PPDM client; exporter binary unchanged.

- `cmd/report` Phase 3: branded backup-assurance report (HTML via html/template + pure-Go PDF
  via maroto v2) over the compliance view, with a computed 3-2-1-1-0 badge (`rule_321110`
  view). Generated via `report render` CLI and an opt-in read-only `GET /report` endpoint.

### Deferred
- OTLP trace spans (diagnostic tracer manager) — metrics OTLP shipped; tracing is a follow-up.
- Backup reporter Phase 4 (scheduling, delivery, multi-tenancy, retention-policy management).
  SLA evaluation (Phase 2) and the branded PDF/HTML report (Phase 3) have shipped.
