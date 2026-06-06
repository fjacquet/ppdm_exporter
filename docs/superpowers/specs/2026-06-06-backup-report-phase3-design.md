# Backup Assurance Reporter — Phase 3: branded report + 3-2-1-1-0 badge

**Date:** 2026-06-06
**Status:** Approved (brainstorming)
**Builds on:** Phase 2 (`docs/superpowers/specs/2026-06-06-backup-report-phase2-design.md`) — the
`compliance` SQL view and `sla_targets`. Phase 1 — the durable history store.
**Branch:** `feat/backup-report-phase3` (off `main`).

## Goal

Turn the live `compliance` verdicts into a **customer-facing branded assurance report** — a
current-snapshot document, per tenant, in **HTML and PDF** — and add a **3-2-1-1-0 backup-rule
badge** computed from the captured data. The report is produced two ways: a `report render` CLI
subcommand (file output) and an on-demand HTTP endpoint on the long-running process.

This is **assurance**, not forensic audit (no signing/WORM). Out of scope: scheduling/delivery,
multi-tenant auth/RBAC, historical trends, ISO/SOC2 narrative mapping (all Phase 4+).

## Decisions (this brainstorm)

1. **HTML + pure-Go PDF** — `html/template` for the rich branded HTML; maroto v2 for a browser-free
   PDF. Both render from one shared `ReportData`, so figures never diverge. No headless Chrome
   (keeps the static, multi-arch build).
2. **3-2-1-1-0 only** — a computed pass/fail badge from captured data (a new SQL view parallel to
   `compliance`). ISO/SOC2 narrative mapping deferred.
3. **Current snapshot** — no historical trend; reads the `compliance` view + `rule_321110` view as-is.
4. **Both CLI and HTTP** — `report render` subcommand (file) plus a read-only `GET /report` endpoint
   served by the capture process when configured.

## Architecture

One data model assembled from SQL, rendered two ways, exposed two ways:

```
compliance view ─┐
rule_321110 view ─┼─► report.Store read methods ─► render.Build(ctx, store, tenant) ─► ReportData
summary counts  ─┘                                                            │
                                                       ┌─────────────────────┴──────────┐
                                                  RenderHTML (html/template)     RenderPDF (maroto v2)
                                                       │                                │
                                            ┌──────────┴───────────┐                    │
                                       CLI `report render`    HTTP GET /report  ◄────────┘
```

A new package **`internal/report/render`** (`package render`) owns `ReportData`, `Build`, and the
two renderers — keeping `internal/report` (capture/store/sla) from growing further. `report.Store`
gains read-only query methods; `render` imports `report`, never the reverse.

### 1. The 3-2-1-1-0 view (`internal/report/migrations.sql`)

A `rule_321110` view, **per asset**, computed live (read-only, recomputes per query), parallel to
`compliance`. Each dimension is a boolean; the heuristics that ride **provisional** `copies` fields
(`2 media`, `1 offsite`) are flagged in the report and here:

| Dim | Column | Rule | Source |
|---|---|---|---|
| 3 copies | `copies_ok` | `count(copies for asset,server) >= 3` | `copies` |
| 2 media | `media_ok` | `count(distinct storage_system_id) >= 2` *(provisional)* | `copies` |
| 1 offsite | `offsite_ok` | `count(distinct location) >= 2` — a 2nd location ≈ offsite/replica *(provisional)* | `copies` |
| 1 immutable | `immutable_ok` | `bool_or(retention_lock)` | `copies` |
| 0 errors | `errors_ok` | `NOT EXISTS` a `backup_jobs` row for the asset with `result_status = 'FAILED'` | `backup_jobs` |

Columns: `tenant, server, asset_id, asset_name, asset_type, copies_ok, media_ok, offsite_ok,
immutable_ok, errors_ok, rule_pass` (= all five), `copies_count, distinct_media, distinct_locations`.
A tenant **badge = PASS** iff every asset has `rule_pass`. NULL-safe like `compliance` (assets with
no copies yield all-false, not NULL).

### 2. `report.Store` read methods

Typed, parameterized (mirroring Phase 2's `$1..$N` discipline), all filtered by tenant:

- `ComplianceRows(ctx, tenant) ([]ComplianceRow, error)` — the per-asset verdict rows from `compliance`.
- `Rule321Rows(ctx, tenant) ([]Rule321Row, error)` — the per-asset rows from `rule_321110`.
- `ReportSummary(ctx, tenant) (Summary, error)` — counts: total assets, compliant %, badge pass/fail,
  per-failing-rule tallies, copy/retention aggregates.

### 3. `internal/report/render`

- **`ReportData`** — `Tenant, BrandName, GeneratedAt`; the compliance snapshot (`Summary` + per-asset
  rows with `Reasons`); the 3-2-1-1-0 badge (overall verdict + per-dimension rollup + failing assets);
  copy/retention evidence rows. `GeneratedAt` is passed in by the caller (tests inject a fixed time).
- **`Build(ctx, store, tenant, brand, now) (ReportData, error)`** — assembles it from the three store
  methods. Returns an error if the tenant has no assets (nothing to report).
- **`RenderHTML(w io.Writer, d ReportData) error`** — `go:embed`'d `html/template` + inline CSS
  (auto-escaped; print-friendly `@media print`). The richer branded document.
- **`RenderPDF(w io.Writer, d ReportData) error`** — maroto v2: a structured, tabular rendering of the
  same `ReportData` (independent layout — the cost of browser-free PDF).

### 4. CLI + HTTP surfaces (`cmd/report`)

- **CLI subcommand** `report render --tenant acme --format html|pdf --out acme.pdf` — `Build` + the
  chosen renderer → file (stdout if `--out` omitted). Reuses the same config (DSN) + `report.Store`.
- **HTTP endpoint** — the long-running capture process *also* serves, when `report.listen` is set,
  `GET /report?tenant=X&format=html|pdf` and `GET /healthz`. Mirrors how the exporter serves
  `/metrics`: served from the same process, started before/alongside the capture loop.
  - **Read-only**, GET only. `tenant` is **required** and validated (non-empty; looked up — unknown
    tenant → 404). `format` defaults to `html`.
  - Binds **`127.0.0.1`** by default. Optional **bearer token** (`report.authToken`): when set,
    requests need `Authorization: Bearer <token>` (constant-time compare) else 401; when empty, no
    auth (localhost-only posture, documented).
  - Sets baseline security headers (`X-Content-Type-Options: nosniff`,
    `Content-Security-Policy: default-src 'none'`, `Cache-Control: no-store`) and the right
    `Content-Type` per format. Gets its own `/security-review` pass before merge.

### 5. Config additions (`internal/config/report.go`)

```yaml
report:
  listen: "127.0.0.1:9103"             # empty = no HTTP endpoint (CLI-only)
  authToken: "${REPORT_TOKEN}"         # optional bearer; empty = no auth (localhost posture)
  brandName: "Acme Backup Assurance"   # report header / branding (default: "Backup Assurance Report")
```

All optional with defaults; `${ENV}` interpolation on `authToken` via the existing loader. An empty
`listen` means the capture process behaves exactly as today (no new surface).

## Error handling

- `render` CLI: a tenant with no assets → clear non-zero-exit error; DB/render errors propagate.
- HTTP: missing/empty `tenant` → 400; unknown tenant → 404; bad/missing token (when configured) → 401;
  render error → 500 (logged, generic body). The endpoint never blocks or crashes the capture loop —
  it runs in its own server goroutine; a handler panic is recovered.
- PDF/HTML render failures are returned, not partial-written (render to a buffer, then copy on success).

## Testing (TDD)

- **`rule_321110` view** (testcontainers): seed copies/jobs and assert each dimension + overall
  `rule_pass` for: full pass, <3 copies, single-media, no-immutable, second-location-missing, and a
  failed-job asset. Plus a no-copies asset (all false).
- **`render.Build`** (testcontainers): assembles `ReportData` from a seeded DB; asserts summary counts,
  badge rollup, and the failing-asset lists.
- **`RenderHTML`**: contains tenant, brand, compliance %, and badge verdict; **escapes a hostile asset
  name** (`<script>` → entity) — the `html/template` XSS guard, asserted explicitly.
- **`RenderPDF`**: produces non-empty bytes starting `%PDF`; no panic on the seeded data.
- **HTTP handler** (`httptest`): 200 + correct `Content-Type` for html/pdf; 400 missing tenant; 404
  unknown tenant; 401 bad token when configured; security headers present; GET-only.
- **config**: `report:` block parses + defaults; `authToken` `${ENV}` interpolation.
- `make ci` parity (gofmt/vet/golangci-lint/test-race/govulncheck); **semgrep clean, no inline
  suppressions**; testcontainers wait-on-second-ready (the Phase 2 fix) reused.

## Demo / verification

- `make demo` already seeds the Gold-VM tenant. Add a one-shot in the compose/docs:
  `report render --tenant acme --format html --out /tmp/acme.html` and confirm it renders the
  compliance verdicts + 3-2-1-1-0 badge (the demo copies — one DD system, one location, one
  immutable copy — yield a realistic **partial** badge: immutable ✓, but <3 copies / single-media /
  single-location ✗).
- If `report.listen` is set in the demo, `curl 127.0.0.1:9103/report?tenant=acme` returns the HTML.
- End-to-end: open the HTML in a browser; generate the PDF and confirm it opens.

## Out of scope (Phase 3)

Scheduling, email/delivery (Phase 4), multi-tenant auth/RBAC, historical trends/charts, retention-policy
management, ISO/SOC2 narrative control mapping, and any cryptographic tamper-evidence (assurance, not
audit). The exporter binary and image stay untouched; the report HTTP surface is opt-in.
