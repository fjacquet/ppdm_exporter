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
    password: "${PPDM1_PASSWORD}"
    insecureSkipVerify: true
```

Run: `make report-cli && PG_PASSWORD=… PPDM1_PASSWORD=… ./bin/report --config config.report.yaml --debug`
(`--once` runs a single capture cycle and exits.)

## Grafana global search

The demo stack (`make demo`) adds a `postgres` service, the `report` capture service, a
Grafana **BackupHistory** Postgres datasource, and a **Backup History** dashboard (jobs +
copies tables with a tenant filter). After one cycle the tables populate from the durable
store — this is the "global search" layer; a signed/branded report is a later phase.

> **Provisional shapes:** `/copies` and `/protection-policies` field names are modeled from
> the 19.22.0 API reference and tagged `// provisional` (one struct + one fixture each) —
> see ADR-0009.

## Assurance report (Phase 3)

Render a tenant's current-snapshot report — SLA compliance verdicts plus a 3-2-1-1-0
backup-rule badge — as branded HTML or pure-Go PDF:

```bash
./bin/report render --tenant acme-corp --format pdf --out acme.pdf
./bin/report render --tenant acme-corp --format html > acme.html
```

When `report.listen` is set, the capture process also serves the report read-only:

```bash
curl -H "Authorization: Bearer $REPORT_TOKEN" 'http://127.0.0.1:9103/report?tenant=acme-corp&format=html'
# (omit -H only if no tokens/authToken is configured — unauthenticated localhost posture)
```

> The 3-2-1-1-0 "2 media" and "1 offsite" checks are best-effort heuristics over provisional
> PPDM copy fields (`storage_system_id`, `location`); the report labels them as such.

## Scheduled delivery (Phase 4a)

Configure SMTP and per-tenant schedules; the report process emails each tenant's report
(HTML body + PDF attachment) on its cadence (daily/weekly/monthly + hour, UTC):

```yaml
smtp: {host: smtp.example.com, port: 587, from: assurance@example.com,
       username: "${SMTP_USER}", password: "${SMTP_PASSWORD}", starttls: true}
schedules:
  - {tenant: acme-corp, cadence: weekly, weekday: Mon, hour: 6, recipients: [ops@acme.com]}
```

Deliveries are recorded in `report_deliveries` (one row per tenant+occurrence); a restart near
the send time won't re-send, and a failed send retries on the next minute-tick until it succeeds.
In the demo, sent mail appears in **Mailpit** at `http://localhost:8025`.

### Per-tenant access (Phase 4b)

Scope `/report` bearer tokens to tenants. `authToken` (if set) is an all-tenants admin token;
each `tokens` entry grants its `token` access to the listed `tenants` (`"*"` = all):

```yaml
report:
  authToken: "${ADMIN_TOKEN}"          # optional, all tenants
  tokens:
    - {token: "${ACME_TOKEN}", tenants: [acme-corp]}
```

```bash
curl -H "Authorization: Bearer $ACME_TOKEN" '127.0.0.1:9103/report?tenant=acme-corp'  # 200
curl -H "Authorization: Bearer $ACME_TOKEN" '127.0.0.1:9103/report?tenant=globex'     # 403
curl '127.0.0.1:9103/report?tenant=acme-corp'                                          # 401 (token required)
```

When no `authToken` and no `tokens` are configured, the endpoint is unauthenticated (localhost posture).

## Retention (Phase 4c)

History is pruned per tenant. `retention.defaultDays` applies to any tenant without an override
(it falls back to `capture.retentionDays` when unset); `overrides` set per-tenant windows:

```yaml
retention:
  defaultDays: 400
  overrides:
    - {tenant: acme-corp, days: 730}
```

Each cycle, `backup_jobs` and `copies` older than the tenant's window are deleted; a tenant's first
capture also backfills only its own window. `assets`/`protection_policies` (current state) are not pruned.
