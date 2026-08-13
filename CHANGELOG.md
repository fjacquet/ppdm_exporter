# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `${VAR:-default}` fallbacks in config env references, ported from `pscale_exporter`.
  Shell / docker-compose semantics: the variable falls back when unset *or* empty, and
  such a reference never aborts startup. A bare `${VAR}` still fails loudly when the
  variable is *unset*; an exported-but-empty one expands to the empty string, as it
  always has.
  Credential fields are stricter: a field written as an env reference that resolves to
  nothing is now rejected, so a stray `PPDM1_PASSWORD=` line fails at startup instead
  of authenticating with an empty credential. The shipped `config.yaml` now uses
  `insecureSkipVerify: "${PPDM1_SKIP_CERTIFICATE:-true}"`, so the setting is env-driven out of the box
  yet still resolves to `true` — this repo's original shipped default — on a host that
  never exported the variable.

## [4.0.0] - 2026-08-01

### Breaking

- The published Docker image's base changes from
  `gcr.io/distroless/static:nonroot` to `alpine:latest`. The container UID
  changes from `65532` to a named user at `10001`. If you pin `runAsUser`,
  `fsGroup`, or similar in your own deployment manifests against the old UID,
  update it. See ADR-0015.

### Added

- `HEALTHCHECK` on both images, checking `/livez`.
- `docker-compose.ghcr.yml` — was missing; pulls the published exporter image
  while keeping `mockppdm`/`report` built locally, matching
  `docker-compose.yml`'s full seven-service topology.

## [3.0.0] - 2026-08-01

### Added

- `/livez` and `/readyz`: probe endpoints that always answer 200, with no
  dependency on server reachability or the collection cycle. See ADR-0014.

### Changed

- `/health` always answers 200, never 503. The JSON body's per-server
  `ok`/`err` fields are unchanged and remain the way to tell whether a
  server is degraded — read the body, not the status code. See ADR-0014.
  Not a breaking change: the path and JSON shape are unchanged.
- The chart's default `livenessProbe`/`readinessProbe` now point at
  `/livez`/`/readyz` instead of `/health`.

## [2.1.0] - 2026-07-14

### Added

- `insecureSkipVerify` in `config.yaml` now accepts a `${VAR}` environment reference
  (e.g. `${PPDM1_SKIP_CERTIFICATE}`) in addition to a native YAML boolean, resolved at
  startup like the other `${PPDM1_*}` fields. Existing native-bool configs keep working.

## [2.0.7] - 2026-07-10

### Security

- Bump Go to 1.26.5 to patch GO-2026-5856 (`crypto/tls`), which `govulncheck`
  flagged in the CI pipeline.

### Fixed

- Restore multi-arch GHCR image publishing by re-adding the `dockers_v2` block to
  `.goreleaser.yaml`; releases now push `ghcr.io/fjacquet/ppdm_exporter` again.
- Fix `Dockerfile.goreleaser` to COPY the per-platform `${TARGETPLATFORM}/ppdm_exporter`
  binary that buildx lays out, instead of a flat path.

## [2.0.6] - 2026-07-03

### Added

- `ppdm_exporter_build_info{version, goversion}` metric (constant `1`) on `/metrics`,
  exposing the running exporter version and Go version so a scrape reveals exactly
  which build is serving metrics. Standard Prometheus build-info pattern.

## [2.0.5] - 2026-07-03

### Documentation

- systemd deployment guide with a sample unit and environment file.
- Design spec for exporter-core family extraction (approach A, recorded but not started).

## [2.0.4] - 2026-07-01

### Fixed

- Bump `golang.org/x/image` to v0.43.0 to address advisory GO-2026-5061.

### Documentation

- Document handling of special characters in the monitoring password.
- Use the brand icon as the MkDocs favicon and logo.

## [2.0.3] - 2026-06-20

### Changed

- Dependency and CI bumps: `testcontainers-go` postgres module and `actions/checkout`.

### Documentation

- Add standard status badges to the README.

## [2.0.2] - 2026-06-20

### Changed

- Migrate CI to the `fjacquet/ci` reusable (make-based) workflows, and make the
  `security` job advisory to match the central default.

## [2.0.1] - 2026-06-16

### Added

- Helm chart with lockstep publishing alongside the binary release.

## [2.0.0] - 2026-06-14

### Changed

- **BREAKING:** the default metrics port is renumbered from `9102` to `9442`, part of a
  family-wide contiguous port block. Scrape configs, firewall rules, and service
  definitions pinning `9102` must be updated.
- Bump Go dependencies.

### Added

- Vendored Node Exporter Full (Grafana 1860) companion dashboard, auto-provisioned in
  the demo stack.

## [1.6.0] - 2026-06-14

### Changed

- Grafana dashboards rechallenged into one consistent product (`grafana/dashboards/`).
  Shared presentation layer across all four boards: canonical activity-status
  semantics (`SUCCESS|OK` / `FAILED` / `RUNNING|QUEUED`) resolving the prior
  Overview-vs-Backup-Report success-rate disagreement, IEC byte / `percentunit` /
  `dtdurations` unit discipline, shared green/amber/red thresholds with
  `colorMode=value`, and table transforms with status value-mapped to colored chips.
  Added cross-board drill-down data links (Overview → Backup Report,
  Compliance → Backup History).
  - **Overview** slimmed to a glance-and-route board: `Servers up` fixed to
    `sum(ppdm_up)`, added `Protected %`, storage units now show `% full` (using the
    previously-unused `ppdm_storage_unit_physical_capacity_bytes`), and panels that
    duplicated Backup Report were removed.
  - **Backup Report** dropped the duplicate success-rate gauge; per-job detail is now
    a single table joined on the `activity_id` label.
  - **SLA Compliance** shows `Compliant %` as `percentunit` with thresholds, moves the
    all-assets table into a collapsed row, and adds true/false compliance chips.
  - **Backup History** grew from 2 to 6 panels: a SQL KPI strip, both tables now honour
    the Grafana time range via `$__timeFilter`, and `$server` / `$result_status`
    template variables were added.
- Design spec recorded at
  `docs/superpowers/specs/2026-06-14-grafana-dashboard-rechallenge-design.md`.

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
