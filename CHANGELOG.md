# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

### Deferred
- OTLP trace spans (diagnostic tracer manager) — metrics OTLP shipped; tracing is a follow-up.
