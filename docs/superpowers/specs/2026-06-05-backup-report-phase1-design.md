# Backup Assurance Reporter — Phase 1: Durable backup-history store

**Date:** 2026-06-05
**Status:** Approved (brainstorming)
**Scope:** Phase 1 of a multi-phase product. This spec covers only the durable history
foundation; SLA evaluation, report rendering, scheduling/multi-tenancy are later phases.

## Product context

A new **assurance / reporting** capability: credibly *show* customers their backups are
running and succeeding against their SLA, over time — and feed both a live Grafana
"global search" view and a branded customer report. It is **not** a forensic/legal audit
system: no cryptographic signing, hash-chaining, WORM, or RFC3161 timestamps (explicitly
de-scoped after recalibration — "audit" was too strong). Provenance is a lightweight
`captured_at` + `source` + tool-version line.

Delivered as a **second binary `cmd/report`** in the existing `ppdm_exporter` repo (one repo,
one shared client, two binaries — the lean exporter image stays unchanged; `cmd/report`
carries the heavier DB/report dependencies). It reuses `internal/ppdmclient` (bearer auth +
pagination), **extended** with the evidence endpoints the exporter does not call.

### Multi-phase roadmap (this spec = Phase 1)

| Phase | Sub-project |
|---|---|
| **1** | **Durable backup-history store** (this spec) |
| 2 | SLA evaluation — per-customer rules over the history; optional framework badge |
| 3 | Branded PDF/HTML report (`cmd/report`) + Grafana dashboards/global-search over Postgres |
| 4 | Multi-tenancy, scheduling, delivery, retention policy management |

## Phase 1 goal

Continuously pull authoritative backup records from each configured PPDM server into a
durable **PostgreSQL** store, retained long-term, tagged per tenant, with provenance — so
downstream phases (and Grafana) have real history to report on. Postgres is chosen because
Grafana reads it natively (first-class datasource) for the live/global-search layer.

## Architecture

`cmd/report` runs a **capture loop** mirroring the exporter's loop/config patterns:

```
capture loop (interval) → per server: ppdmclient pulls activities, copies, assets,
  protection-policies (incremental by watermark) → upsert into Postgres (tenant-tagged,
  provenance-stamped) → prune beyond retention
                                   │
                                   └── Grafana (Postgres datasource) — live status & global search
```

- One capture goroutine per server (errgroup, bounded), like the exporter; a server failure
  degrades that server's capture, never the others.
- Capture is **incremental**: a per-server, per-resource watermark (max `createdAt`/`createTime`
  seen) drives a `filter=... ge "<watermark>"` query so each cycle fetches only new records.
- **Idempotent upserts** keyed by the PPDM record `id` — re-capturing a record is a no-op.

### Data model (PostgreSQL)

Grounded in the PPDM 19.22.0 API (fields confirmed in `/tmp/ppdm.txt`). All shapes the
exporter has not yet exercised are **provisional** and tagged in code; validation joins the
exporter's ADR-0009 list.

- **`backup_jobs`** (from `/api/v2/activities`; append-only — jobs are immutable events):
  `id PK, tenant, server, category, subcategory, result_status, asset_id, asset_name,
  policy_name, started_at, completed_at, bytes_transferred, created_at, captured_at`.
- **`copies`** (from `/api/v2/copies`; append-only): `id PK, tenant, server, asset_id,
  policy_name, copy_type, create_time, expiration_time, retention_time, retention_lock bool,
  storage_system_id, location, size_bytes, captured_at`. *This table answers "how many copies,
  retained until when, where (incl. replication/offsite), immutable?" — the core of SLA proof.*
- **`assets`** (from `/api/v2/assets`; upsert-latest current state): `id PK, tenant, server,
  name, type, protection_status, last_available_copy_time, policy_name, updated_at, captured_at`.
- **`protection_policies`** (from `/api/v3/protection-policies`; upsert-latest): `id PK, tenant,
  server, name, objectives_json (stages/schedule/interval/retention), updated_at, captured_at`.
  Stored as structured columns where stable + a `jsonb` blob for the nested objectives/stages.
- **`capture_runs`** (provenance): `id, server, started_at, finished_at, ok bool, error,
  counts_json (per-resource inserted/updated), tool_version`.

`tenant` is derived from config (per-server mapping in Phase 1); the column exists on every
table so Phase 4 multi-tenancy needs no schema reshape.

### PPDM client extension (`internal/ppdmclient`)

Add typed list helpers the exporter doesn't use, reusing the existing `GetAll[T]` paginator
and bearer auth — no second client:
- `/api/v2/copies` (copy records),
- `/api/v3/protection-policies` (policy objectives),
- richer `/api/v2/activities` fields already added in the exporter's per-job work,
- `/api/v2/assets` already covered.

The exporter is unaffected (it simply doesn't call the new helpers).

### Configuration

A `cmd/report` config (same YAML + `${ENV}`/`passwordFile` loader, extended):

```yaml
database:
  dsn: "postgres://user:${PG_PASSWORD}@localhost:5432/backup_report?sslmode=disable"
capture:
  interval: "1h"
  timeout: "5m"
  retentionDays: 400      # prune backup_jobs/copies older than this (days; ~13 months)
servers:
  - name: ppdm-prod-01
    tenant: acme-corp      # customer tag for all records from this server
    host: ppdm01.example.com
    port: 8443
    username: ppdm-monitor
    password: "${PPDM01_PASSWORD}"
    insecureSkipVerify: true
```

## Error handling

- Per-server capture failure is logged and recorded in `capture_runs` (ok=false); other
  servers proceed. The watermark only advances for resources that captured successfully, so a
  failed cycle re-fetches next time (no gaps).
- Client retries 5xx only, never 4xx; re-login on expiry/401 (inherited from `ppdmclient`).
- DB writes are transactional per resource batch; a failed batch rolls back and the watermark
  is not advanced.
- Migrations run on startup and are idempotent; a migration failure aborts startup.

## Testing

- TDD. PPDM pulls are driven by the in-memory `ppdmclient.Mock` (and `httptest` for the new
  endpoints) against `// provisional` fixtures.
- DB layer tested against a real Postgres via **`testcontainers-go`** (self-contained — spins
  up Postgres in Docker for the test, no external service), asserting: idempotent upsert
  (re-capture is a no-op), watermark advance, retention prune, tenant tagging, provenance rows.
  Tests that require Docker are guarded so `go test -short` skips them.
- `make ci` parity: gofmt, vet, golangci-lint, `go test -race`, govulncheck; semgrep clean
  (no inline suppressions).

## Demo / verification

The docker-compose stack gains a `postgres` service and a `report` service running the
capture loop against `mockppdm` (its fixtures extended with copies + protection-policies),
plus a Grafana **Postgres** datasource and a starter "Backup History" dashboard (global
search: jobs/copies by tenant/asset/policy/status over time). End-to-end check: after one
capture cycle, Grafana shows rows from the durable store, and `capture_runs` records the run.

## Out of scope (Phase 1)

SLA evaluation (P2), branded report rendering (P3), scheduling/delivery + full multi-tenancy
(P4), direct Data Domain capture (PPDM `/copies` already carries location + retention, so DD
isn't needed yet), and all cryptographic tamper-evidence (dropped in recalibration).

## Decisions (this brainstorm)

1. **Assurance, not audit** — show proper backup success/behaviour; no signing/WORM/RFC3161/hash-chain.
2. **Authoritative API pull** — real PPDM records (activities/copies/assets/policies), not derived metrics.
3. **One repo, shared extended `ppdmclient`, two binaries** — lean exporter unchanged; new `cmd/report`.
4. **Postgres** — durable history + native Grafana datasource for the global-search layer.
5. **Both deliverables** — Grafana (live + search) and a generated branded report (P3).
6. **Policy + framework badging** model for SLA (P2).
