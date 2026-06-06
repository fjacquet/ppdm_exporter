# Backup Assurance Reporter — Phase 4c: per-tenant retention management

**Date:** 2026-06-06
**Status:** Approved (brainstorming)
**Builds on:** Phase 1 (`Store.Prune`, `Capturer.bootstrap`, `capture.retentionDays`). Phase 4
decomposition: 4a (delivery, shipped), 4b (per-tenant access control, shipped), 4c (this — the last
Phase 4 piece).
**Branch:** `feat/backup-report-phase4c` (off `main`).

## Goal

Today a single global `capture.retentionDays` window prunes `backup_jobs`/`copies` for every tenant
and also sets every tenant's first-capture backfill window. Phase 4c makes retention **per-tenant**:
a default plus per-tenant overrides, applied to both the prune and each tenant's backfill — so
different customers can keep history for different durations (e.g. a contractual 2-year window).

## Decisions (this brainstorm)

1. **Per-tenant only** — one window per tenant (default + overrides), applied to both jobs and copies.
   Per-resource (jobs vs copies) and immutable-aware pruning are out of scope.
2. **Back-compat** — a new `retention` config block; `retention.defaultDays` falls back to the existing
   `capture.retentionDays` (which keeps defaulting to 400). Existing configs work unchanged.
3. **Prune covers all tenants** — per-tenant cutoffs for override tenants plus a default sweep for the
   rest (including tenants present in data but absent from config). `assets`/`protection_policies`
   stay unpruned, as today.

## Architecture

```text
config.Retention{DefaultDays, Overrides[]} ── DaysFor(tenant) ─┐
                                                               ├─► Capturer.bootstrap(tenant) (backfill window)
cmd/report: NewCapturer(store, version, cfg.Retention, cfg.Compliance)
                                                               └─► RunOnce -> Store.Prune(defaultDays, overrides)
```

### 1. Config (`internal/config/report.go`)

```yaml
retention:
  defaultDays: 400                 # if unset, falls back to capture.retentionDays (default 400)
  overrides:
    - {tenant: acme-corp, days: 730}
```

New types and method:

```go
type RetentionOverride struct {
	Tenant string `yaml:"tenant"`
	Days   int    `yaml:"days"`
}
type Retention struct {
	DefaultDays int                 `yaml:"defaultDays"`
	Overrides   []RetentionOverride `yaml:"overrides"`
}
// DaysFor returns the retention window for a tenant: its override if present, else DefaultDays.
func (r Retention) DaysFor(tenant string) int
```

`Retention Retention \`yaml:"retention"\`` on `ReportConfig`. In `LoadReport`, after the capture
defaults: if `cfg.Retention.DefaultDays == 0`, set it to `cfg.Capture.RetentionDays` (which is itself
already defaulted to 400). Then validate `DefaultDays > 0` (after the fallback) and that each override
has a non-empty `Tenant` and `Days > 0`. Empty `retention` block ⇒ `DefaultDays` =
`capture.retentionDays`, no overrides ⇒ behaves exactly as today.

`DaysFor`: returns the matching override's `Days`, else `DefaultDays`.

### 2. Capturer (`internal/report/capture.go`)

Replace the `retentionDays int` field with `retention config.Retention`:

```go
func NewCapturer(store *Store, version string, retention config.Retention, compliance config.Compliance) *Capturer
```

- **`bootstrap(tenant string, wm time.Time) time.Time`** — gains a `tenant` param; when `wm.IsZero()`
  it returns `time.Now().AddDate(0, 0, -c.retention.DaysFor(tenant))` (per-tenant backfill window).
  `capture(...)` already has `tenant` in scope, so its two `c.bootstrap(...)` calls pass it.
- **`RunOnce`** — after the capture errgroup, build `overrides := map[string]int` from
  `c.retention.Overrides` and call `c.store.Prune(ctx, c.retention.DefaultDays, overrides)`.

### 3. Store prune (`internal/report/store.go`)

```go
// Prune deletes append-only event rows (backup_jobs, copies) older than each tenant's retention
// window: per-tenant for override tenants, then a default sweep for all other tenants. assets and
// protection_policies hold current state and are intentionally not pruned.
func (s *Store) Prune(ctx context.Context, defaultDays int, overrides map[string]int) error
```

Implementation (parameterized; bounded to `len(overrides)+1` deletes per table):

- For each `(tenant, days)` in `overrides`:
  `DELETE FROM backup_jobs WHERE tenant=$1 AND created_at < $2` and
  `DELETE FROM copies WHERE tenant=$1 AND create_time < $2`, with `$2 = now - days`.
- A default sweep over the remaining tenants, using a `text[]` of the override tenant names:
  `DELETE FROM backup_jobs WHERE tenant <> ALL($1::text[]) AND created_at < $2` and the `copies`
  equivalent, with `$2 = now - defaultDays`. When `overrides` is empty the array is empty and
  `tenant <> ALL('{}')` is true for every row, so this degenerates to the current global prune.

> Cutoffs are computed in Go (`time.Now().AddDate(0,0,-days)`) and passed as parameters, mirroring the
> existing Prune. NULL `created_at`/`create_time` rows are not matched by `< cutoff` (same as today).

### 4. Wiring (`cmd/report/main.go`)

`capt := report.NewCapturer(store, version, cfg.Retention, cfg.Compliance)` (was the `int`
`cfg.Capture.RetentionDays`). No other call-site change in main. The existing `NewCapturer(st, "v",
400, …)` test call sites change to `NewCapturer(st, "v", config.Retention{DefaultDays: 400}, …)`.

## Error handling

- A prune failure for one statement returns the error (logged by `RunOnce`'s existing `WithError`
  warning); capture is unaffected (prune runs after capture).
- Per-tenant deletes are independent statements; a failure aborts the remaining deletes for that cycle
  but the next cycle retries (prune is idempotent — re-deleting already-gone rows is a no-op).
- `DaysFor` never returns a non-positive window: validation guarantees every override `Days > 0` and
  `DefaultDays > 0` (after the `capture.retentionDays` fallback), so no cutoff is ever in the future.

## Testing (TDD)

- **Config**: `retention` parse; `DefaultDays` back-compat fallback from `capture.retentionDays`;
  `DaysFor` (override hit, default miss); validation (override `Days <= 0`, empty tenant, negative
  `defaultDays`).
- **Prune** (testcontainers): seed tenant `acme` (override 730d) and `globex` (default 400d), each with
  an old row (~500 days) and a recent row. After `Prune(400, {acme:730})`: `acme`'s 500-day row
  **survives** (within 730), `globex`'s 500-day row is **deleted**, both recent rows survive. Assert on
  both `backup_jobs` and `copies`. Plus the empty-overrides case = global prune at `defaultDays`.
- **bootstrap**: `DaysFor` drives the per-tenant backfill window (unit on `DaysFor`; the existing
  capture tests continue to pass through `bootstrap(tenant, wm)`).
- `make ci` parity (gofmt/vet/golangci-lint/test-race/govulncheck); **semgrep clean, no inline
  suppressions**.

## Demo / verification

- `config.report.demo.yaml` gains a `retention` block with a per-tenant override for `acme-corp` (e.g.
  `days: 730`) over a `defaultDays: 400`, documented. `docs/report.md` documents `retention` and the
  per-tenant prune. The demo's short cycle still prunes nothing visible (fixtures are recent), so the
  verification is the unit/testcontainer tests plus a documented `SELECT count(*) … WHERE tenant=…`
  check after a manual old-row insert.

## Out of scope (4c)

Per-resource windows (jobs vs copies), immutable-aware pruning (`retention_lock`), pruning of
`assets`/`protection_policies`/`capture_runs`/`report_deliveries`, dynamic/per-policy retention, and a
retention-management UI. The exporter binary/image are untouched.
