# Backup history (`cmd/report`)

`cmd/report` is a second binary in this repo (the lean exporter is unaffected). It
periodically captures **authoritative** PowerProtect Data Manager records into PostgreSQL,
retained long-term, so they can back an assurance report and a Grafana global-search view.
It is **Phase 1** of a backup-assurance reporter — SLA evaluation and a branded report come
in later phases.

> This is **assurance**, not forensic audit: no signing / WORM / timestamps. Provenance is a
> lightweight `captured_at` + `source` + tool-version (`capture_runs` table).

## What it captures

Each cycle, per configured server, it pulls via the shared `ppdmclient` (bearer auth +
pagination — no separate client):

| Resource | Endpoint | Table | Mode |
|---|---|---|---|
| Jobs | `/api/v2/activities` | `backup_jobs` | incremental (createdAt watermark), append-only |
| Copies | `/api/v2/copies` | `copies` | incremental (createTime watermark), append-only |
| Assets | `/api/v2/assets` | `assets` | full pull, upsert-latest |
| Policies | `/api/v3/protection-policies` | `protection_policies` | full pull, upsert-latest |
| Provenance | — | `capture_runs` | one row per server per cycle |

`copies` is the core of SLA proof — copy type, retention/expiry, `retention_lock`, location
(incl. replication/offsite), and size. Every row is tagged with a `tenant` (per-server in
Phase 1) so multi-tenancy slots in later. Rows older than `retentionDays` are pruned.

## Configuration

```yaml
database:
  dsn: "postgres://report:${PG_PASSWORD}@localhost:5432/backup_report?sslmode=disable"
capture:
  interval: "1h"
  timeout: "5m"
  retentionDays: 400        # ~13 months
servers:
  - name: ppdm-prod-01
    tenant: acme-corp        # customer tag for all records from this server
    host: ppdm01.example.com
    port: 8443
    username: ppdm-monitor
    password: "${PPDM01_PASSWORD}"
    insecureSkipVerify: true
```

Run: `make report-cli && PG_PASSWORD=… PPDM01_PASSWORD=… ./bin/report --config config.report.yaml --debug`
(`--once` runs a single capture cycle and exits.)

## Grafana global search

The demo stack (`make demo`) adds a `postgres` service, the `report` capture service, a
Grafana **BackupHistory** Postgres datasource, and a **Backup History** dashboard (jobs +
copies tables with a tenant filter). After one cycle the tables populate from the durable
store — this is the "global search" layer; a signed/branded report is a later phase.

> **Provisional shapes:** `/copies` and `/protection-policies` field names are modeled from
> the 19.22.0 API reference and tagged `// provisional` (one struct + one fixture each) —
> see ADR-0009.
