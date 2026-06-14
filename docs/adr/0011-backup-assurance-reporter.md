# Backup-assurance reporter: second binary, Postgres persistence, assurance scope

## Status
Accepted.

## Context
The exporter answers "what is the live state of PPDM right now" — it is stateless by design
(ADR-0002: a snapshot is built per cycle and pointer-swapped; nothing is retained between
cycles, and metrics are at most one interval stale). A second, distinct need emerged:
**long-term, authoritative backup history** — jobs, copies, asset protection state, and
policies retained for months — to back an assurance report and a Grafana global-search view.
Prometheus is the wrong store for this: it samples gauges, drops to staleness on scrape
gaps, and is not a system of record for per-object historical events.

This is **assurance**, not forensic audit. The goal is "can we show what PPDM reported over
time", not "can we prove in court that a record was not tampered with."

## Decision
Build `cmd/report` as a **second binary in the same repo**, leaving the exporter binary and
image untouched. It reuses the shared `internal/ppdmclient.GetAll` (bearer auth + pagination,
no client change) and `internal/config` (`LoadReport`); `internal/report` holds the capture
DTOs, the `pgx` `Store`, and the `Capturer` loop.

- **PostgreSQL via `pgx`** is the system of record — a deliberate departure from the
  stateless snapshot model (ADR-0002). `migrations.sql` is `//go:embed`-ed and applied
  idempotently (`CREATE TABLE IF NOT EXISTS`); writes are idempotent upserts keyed by
  `(id, server)` so re-capture is a no-op update. Incremental capture uses a per-resource
  watermark; old rows are pruned.
- **Append-only event tables** (`backup_jobs`, `copies`) plus **current-state tables**
  (`assets`, `protection_policies`); `capture_runs` records lightweight provenance.
- **Assurance, not forensic**: provenance is a lightweight `captured_at` + `source` +
  tool-version triple in `capture_runs`. **No crypto, signing, WORM, or audit machinery.**
- **Grafana reads Postgres directly** (BackupHistory datasource) for the global-search view,
  bypassing the exporter / Prometheus / OTLP path entirely.
- **DB tests use `testcontainers-go`**, skipped under `-short` (the exporter uses
  `httptest` + `ppdmclient.Mock`; a real Postgres is needed to exercise SQL and upserts).

## Consequences
Two binaries with different durability models coexist: the exporter stays stateless and
lean; the reporter owns a database. The provisional-API discipline (ADR-0009/0010) still
applies — capture DTOs are isolated structs and carry `// provisional` / ADR-0010 notes.
Operators must provision and back up Postgres for the reporter (not for the exporter). The
assurance/forensic boundary is load-bearing: adding signing or WORM later is a new decision,
not a tweak. SLA evaluation and a branded report are later phases; this ADR covers Phase 1
capture only.
