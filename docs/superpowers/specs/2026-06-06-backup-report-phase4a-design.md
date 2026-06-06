# Backup Assurance Reporter — Phase 4a: scheduled report generation + delivery

**Date:** 2026-06-06
**Status:** Approved (brainstorming)
**Builds on:** Phase 3 (`docs/superpowers/specs/2026-06-06-backup-report-phase3-design.md`) — `render.Build`,
`RenderHTML`/`RenderPDF`. Phase 1 store + provenance pattern (`capture_runs`).
**Branch:** `feat/backup-report-phase4a` (off `main`).

## Scope

Phase 4 in the roadmap bundles four subsystems; it is decomposed into independent sub-projects,
each with its own spec → plan → implement cycle:

- **4a (this spec)** — scheduled report generation + email delivery.
- **4b** — per-tenant access control on the `/report` endpoint.
- **4c** — per-tenant / per-resource retention-policy management.

## Goal

Periodically (per-tenant calendar cadence) generate each tenant's Phase-3 assurance report and
**deliver it by email**, restart-safely and auditably — so customers receive their backup-assurance
report on a schedule without anyone running a command. In-process scheduler in the existing report
binary, alongside the capture loop. Email is the only channel in 4a, behind a `Deliverer` interface
so file/webhook channels drop in later.

## Decisions (this brainstorm)

1. **In-process scheduler** — a new loop in the report process, mirroring the capture loop; cadence is
   config-driven (not external cron).
2. **Email/SMTP only** in v1, behind a `Deliverer` interface.
3. **Presets cadence** — `daily | weekly | monthly` + send `hour` (+ `weekday`/`day`), UTC. No
   cron-expression dependency.
4. **`report_deliveries` table** — dedupe (no double-send across restarts/ticks) + provenance.
5. **Mail library** — `github.com/wneessen/go-mail` (pure-Go, correct MIME/attachment/STARTTLS) on the
   report binary only; the lean exporter image is untouched.

## Architecture

```text
report process:
  capture loop (existing) ── every cfg.capture.interval
  schedule loop (NEW)     ── ticks every tickInterval (1m):
     for each schedule (per tenant):
        period := PeriodKey(now, sched)                       # date | ISO-week | month occurrence
        if now >= ScheduledTime(now, sched) && !store.DeliveryExists(ctx, tenant, period):
            data := render.Build(ctx, store, tenant, brand, now)   # Phase 3
            html := render.RenderHTML(data); pdf := render.RenderPDF(data)
            err  := deliverer.Deliver(ctx, tenant, sched.Recipients, subject(data), html, pdf)
            store.RecordDelivery(ctx, tenant, period, err == nil, errStr, sched.Recipients)
```

"Due" = `now >= scheduled-time-of-the-current-period AND no delivery row for (tenant, period)`. This
gives **restart catch-up** (down at 06:00 → sends on the next tick within the same period) and
**no double-send**. All times **UTC** (per-tenant timezone is a later refinement, documented).

### 1. `internal/report/schedule` (new package)

Pure cadence functions (no DB/SMTP — unit-testable):

- `PeriodKey(now time.Time, s config.Schedule) string` — the occurrence key:
  daily → `2006-01-02`, weekly → `2006-W03` (ISO year-week), monthly → `2006-01`.
- `ScheduledTime(now time.Time, s config.Schedule) time.Time` — the send instant for the occurrence
  containing `now`: daily → today @ `hour`; weekly → this ISO week's `weekday` @ `hour`; monthly →
  this month's `day` @ `hour` (a `day` past month length clamps to the last day).
- `Due(now time.Time, s config.Schedule) bool` — `!now.Before(ScheduledTime(now, s))`.

Plus the orchestrator:

- `Scheduler` holds `store *report.Store`, `deliverer delivery.Deliverer`, `schedules []config.Schedule`,
  `brand string`, `tick time.Duration`.
- `New(store, deliverer, schedules, brand) *Scheduler`.
- `Run(ctx)` — ticker loop; on each tick calls `runDue(ctx, now)`; returns on `ctx.Done()`.
- `runDue(ctx, now)` — for each schedule: if `Due` and not `DeliveryExists`, build → render → deliver →
  `RecordDelivery`. A failure for one tenant is logged and recorded (`ok=false`); it does not block
  other tenants and is retried next tick (no row was written for the period on failure, OR the row is
  written with `ok=false` and retried — see Error handling). Imports `report`, `render`, `delivery`,
  `config`; nothing imports it back (no cycle).

### 2. `internal/report/delivery` (new package)

```go
type Deliverer interface {
    Deliver(ctx context.Context, tenant string, to []string, subject string, html, pdf []byte) error
}
```

- `SMTP` implementation built from `config.SMTP`, using `go-mail`: a `multipart/alternative`-ish message
  with an HTML body and a `application/pdf` attachment (`<tenant>-<period>.pdf`), STARTTLS + auth.
- Message composition is factored so it can be asserted without a live server; one end-to-end send is
  exercised against a small in-process SMTP listener in tests.
- The interface keeps file/webhook channels (4-later) drop-in; only `SMTP` ships in 4a.

### 3. Store additions (`internal/report/migrations.sql` + `store.go`)

```sql
CREATE TABLE IF NOT EXISTS report_deliveries (
  tenant text NOT NULL,
  period text NOT NULL,                       -- occurrence key (date / ISO-week / month)
  sent_at timestamptz NOT NULL DEFAULT now(),
  ok boolean NOT NULL,
  error text,
  recipients text,                            -- comma-joined, for the audit trail
  PRIMARY KEY (tenant, period)
);
```

- `DeliveryExists(ctx, tenant, period string) (bool, error)` — returns true only for a **successful**
  prior delivery (so failed attempts retry): `SELECT EXISTS(SELECT 1 FROM report_deliveries WHERE
  tenant=$1 AND period=$2 AND ok)`.
- `RecordDelivery(ctx, tenant, period string, ok bool, errMsg string, recipients []string) error` —
  idempotent upsert on `(tenant, period)` (a failed row is overwritten by a later success).

### 4. Config additions (`internal/config/report.go`)

```yaml
smtp:
  host: smtp.example.com
  port: 587
  from: "assurance@example.com"
  username: "${SMTP_USER}"
  password: "${SMTP_PASSWORD}"   # ${ENV} interpolated, like report.authToken
  starttls: true
schedules:
  - tenant: acme-corp
    cadence: weekly              # daily | weekly | monthly
    weekday: Mon                 # weekly only (Mon..Sun); ignored otherwise
    day: 1                       # monthly only (1..31, clamped to month length); ignored otherwise
    hour: 6                      # 0..23, UTC
    recipients: [ops@acme.com]
```

Every email is **an HTML body (the rendered report) + a PDF attachment** — no per-schedule format
selector in 4a (it added branching for little value; html-only / pdf-only is a later refinement).

New types: `SMTP` struct and `Schedule` struct; `Schedules []Schedule` and `SMTP SMTP` on
`ReportConfig`. `LoadReport` interpolates `smtp.password`/`username`, and **validates only when
`schedules` is non-empty**: `smtp.host` and `smtp.from` set; each schedule has a known `cadence`,
`hour` in 0..23, non-empty `recipients`, and a valid `weekday` (weekly) / `day` 1..31 (monthly).
Empty `schedules` ⇒ scheduler never starts (process behaves as today).

### 5. Wiring (`cmd/report/main.go`)

Mirrors the Phase-3 HTTP server: when `len(cfg.Schedules) > 0`, construct `delivery.NewSMTP(cfg.SMTP)`,
`sched := schedule.New(store, deliverer, cfg.Schedules, cfg.Report.BrandName)`, and `go sched.Run(ctx)`
alongside the capture loop. `--once` skips the scheduler (it is a long-running concern). The `render`
CLI subcommand and `/report` endpoint are unchanged.

## Error handling

- A per-tenant delivery failure (render or SMTP) is logged and recorded as `ok=false` with the error;
  it does **not** block other tenants. Because `DeliveryExists` only counts `ok=true`, a failed
  occurrence is retried on subsequent ticks until it succeeds or the occurrence's period rolls over.
- `render.Build` returning `ErrNoData` (tenant has no captured assets yet) is logged and recorded as a
  skip (`ok=false`, error "no data"); it retries next tick (capture may populate it).
- SMTP uses STARTTLS + auth; the password comes from `${ENV}`. A misconfigured SMTP block surfaces at
  first send (logged + recorded), never panics the loop.
- The scheduler runs in its own goroutine; a tenant panic is contained per iteration (recovered, logged)
  so one bad tenant cannot kill the loop.

## Testing (TDD)

- **Cadence** (pure unit, no DB/SMTP): `PeriodKey`/`ScheduledTime`/`Due` for daily/weekly/monthly —
  before/after the send hour, weekday match, month-day clamp (e.g. `day:31` in February), week/month
  rollover, and the catch-up case (now well past the scheduled time, same period).
- **Scheduler** (testcontainers + a fake `Deliverer`): a due tenant → `Deliver` called once and an
  `ok=true` row recorded; a second tick in the same period → **not** re-sent; a `Deliver` error →
  `ok=false` row, and the next tick **retries**; `ErrNoData` tenant → recorded skip, no `Deliver` call.
- **SMTP** (`delivery`): assert the composed message has the HTML body + the `application/pdf`
  attachment + subject (compose-only); one end-to-end send against an in-process SMTP listener.
- **Config**: `smtp` + `schedules` parse and validation errors (unknown cadence, bad hour, empty
  recipients, missing smtp.host when schedules present).
- `make ci` parity (gofmt/vet/golangci-lint/test-race/govulncheck); **semgrep clean, no inline
  suppressions**; `report_deliveries` covered; reuse the testcontainers second-ready wait.

## Demo / verification

- `make demo` adds a `mailpit` service (SMTP sink + web UI on :8025) and a demo `smtp:` block + a
  `schedules:` entry for `acme-corp` (cadence `daily`, `hour` set to the current hour for an immediate
  send in the demo). After a capture cycle the scheduler emails the report; open Mailpit at
  `http://localhost:8025` to see the message with the PDF attachment. `make demo` banner gains the
  Mailpit URL.
- End-to-end: `SELECT tenant, period, ok FROM report_deliveries` shows one `ok=true` row; a second
  scheduler tick adds no new row.

## Out of scope (4a)

Per-tenant access control (4b), per-tenant/per-resource retention (4c), non-email channels
(file/webhook — the `Deliverer` interface is ready, not implemented), per-schedule format selection
(always HTML body + PDF attachment in 4a), per-tenant timezones, retry/backoff policy beyond next-tick
retry, and a delivery-history dashboard. The exporter binary/image are untouched.
