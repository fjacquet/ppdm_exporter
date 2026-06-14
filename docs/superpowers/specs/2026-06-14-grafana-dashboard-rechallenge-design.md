# Grafana dashboard rechallenge — design

**Date:** 2026-06-14
**Status:** Approved (design); pending implementation plan
**Scope:** All four dashboards in `grafana/dashboards/` — Overview, Backup Report,
SLA Compliance, Backup History.
**Audience:** Mixed ops + management (operators plus a glanceable exec/SLA layer).
**Constraints:** Free rein on structure (may add/remove/merge panels, add template
variables, add data links, change panel types and queries). Logic bugs may be fixed.

## Goal

Make the four dashboards **crisp, professional, focused, and logically consistent** —
one product, not four ad-hoc boards. Establish a shared presentation layer, remove
duplication and contradictions, and give each board a single clear job with a
top-to-bottom hierarchy and drill-down links between them.

## Audit findings (the "rechallenge")

### Cross-cutting

1. **Inconsistent "success" definition.** Overview's PROTECT success rate uses
   `result_status="SUCCESS"`; Backup Report uses `result_status=~"SUCCESS|OK"`. Same
   concept, two answers.
2. **Duplicated panels across boards.** "Failed jobs by category", "success rate",
   and "data transferred/protected" appear in both Overview and Backup Report with
   slightly different queries — numbers can disagree, no single source of truth.
3. **Two disconnected data families.** Prometheus boards key on `$server`; Postgres
   boards (History, Compliance) key on `$tenant`. No drill-down links, so there is no
   narrative path Overview → Report → History.
4. **Raw-metric tables with no transforms.** `ppdm_asset_last_copy_age_seconds`,
   `ppdm_health_entity_status`, and the per-job tables dump Time/Value + every label.
5. **Inconsistent units/thresholds/color.** Bytes sometimes raw, ratios sometimes
   0–1 without `percentunit`, no shared green=good/red=bad semantics.

### Per-dashboard

- **Overview (24 panels):** too dense for a glance board; embeds deep tables that
  belong in detail boards. "Storage units — physical used" ignores the available
  `ppdm_storage_unit_physical_capacity_bytes`, so % full is not shown. "Servers up"
  plots `ppdm_up` per-series instead of a count.
- **Backup Report (14):** "Success rate" exists twice (identical stat + gauge). The
  per-job table stitches three queries (`info`/`bytes`/`duration`) with no join.
- **Compliance (6):** "Compliant" stat is `avg(compliant::int)` (a 0–1 ratio) but
  labeled like a count and lacks `percentunit`. Two largely overlapping tables.
- **Backup History (2):** raw `LIMIT 500`, ignores Grafana's time range
  (`$__timeFilter`), no KPI strip, no tenant/result facets.

### Confirmed NOT a bug (preserve)

`ppdm_asset_last_copy_age_seconds` is **sparse by design** —
`internal/ppdm/assets.go` emits it only for at-risk assets (unprotected, or
`age > AgeThreshold`). The Overview "Assets at risk" `count(...)` is therefore
correct and must be preserved. Document this so it is not "fixed" later.

## Design principles (shared "pro" layer, applied to all four)

- **Canonical status semantics** as dashboard constants / reused regex:
  `success = SUCCESS|OK`, `failed = FAILED`, `active = RUNNING|QUEUED`.
- **Unit discipline:** bytes → `bytes` (IEC); ratios → `percentunit` (0–1); ages →
  `s` / `dtdurations`. No raw numbers where a unit exists.
- **Shared thresholds & color:** green = healthy, amber = warn, red = breach;
  `color mode = value` on stats; same threshold steps across boards.
- **Tables get transforms:** `labels to fields` / `organize` / `sort by`, unit'd
  columns, no Time/Value dumps; status cells value-mapped to colored chips.
- **One KPI strip per board** (stat row at top), then breakdowns, then detail tables —
  strict top-to-bottom hierarchy.
- **Cross-board drill-downs** via Grafana data links: Overview → Backup Report
  (filtered by category/server) → Backup History (filtered by tenant);
  Compliance → Backup History.
- **Consistent variables:** `$server` (Prometheus boards), `$tenant` (Postgres
  boards), plus shared `$category` / `$result_status` where relevant; all
  multi-select with an "All" option.

## Per-dashboard redesign

### 1. Overview — "glance & route" (~16 panels, down from 24)

Role: landing board. Exec KPIs + health; links out for depth instead of embedding it.

- **KPI strip (stat row):** Servers up (`sum(ppdm_up)`) · Collectors degraded ·
  Protected % (protected / total, `percentunit`) · Assets at risk · Critical alerts ·
  Success rate 24h (canonical regex).
- **Backup activity:** outcomes-over-time, data transferred by category, PROTECT
  success gauge — each with a data link to Backup Report (carrying `$server`,
  `$category`).
- **Asset protection:** status piechart · by-type bargauge · at-risk table
  (transformed: asset, type, age as `dtduration`, sorted desc).
- **Capacity:** system % used gauge (`percentunit`) · used-vs-total trend · storage
  units as **% full** (`physical_used / physical_capacity`, `percentunit`).
- **Health & alerts:** health entities (transformed, status chips) · open alerts by
  severity · alerts-over-time by severity.
- **Removed:** "Activity breakdown" table and "Failed jobs by category" (now owned by
  Backup Report).

### 2. Backup Report — "what happened" (~11 panels, down from 14)

- **KPI strip:** Total · Succeeded · Failed · Success rate · Data protected · Active —
  all using canonical status regex.
- **Remove** the duplicate Success-rate gauge; keep the stat.
- Outcomes by category/status table (transformed) · failed-by-category bargauge ·
  data-by-category pie · outcomes-over-time · data-over-time.
- **Per-job detail:** a **single table** built with `join`/`merge` transform on job id
  across `ppdm_activity_info` + `ppdm_activity_job_bytes` +
  `ppdm_activity_job_duration_seconds`, behind the existing `perJobActivities` row.
  Replaces the fragile three-query stitch.

### 3. SLA Compliance — "are we meeting SLA" (keep 6, fix logic)

- "Compliant" stat → `percentunit`, relabeled "Compliant %", thresholds (red < 0.95).
- "Compliant % by tenant" bargauge → `percentunit`, same thresholds.
- Keep both tables, but move "All assets — resolved targets & verdicts" into a
  collapsed row so the non-compliant table leads.
- Data links from non-compliant rows → Backup History filtered by that tenant.

### 4. Backup History — "forensic search" (~6 panels, up from 2)

- **KPI strip from SQL:** jobs in range · failed · bytes transferred · distinct assets
  (all respecting `$__timeFilter` + `$tenant` + `$result_status`).
- Both tables honor Grafana time range via `$__timeFilter(created_at)` /
  `$__timeFilter(create_time)` instead of blind `LIMIT 500` (keep a high LIMIT as a
  safety cap).
- Add `$result_status` and `$server` template vars alongside `$tenant`.
- Status columns value-mapped to colored chips; bytes columns unit'd (`bytes`).

## Components and boundaries

Each dashboard JSON is an independent unit with one job:

| Dashboard | Job | Data source | Key vars |
|---|---|---|---|
| Overview | Glance + route | Prometheus | `$server` |
| Backup Report | What happened | Prometheus | `$server`, `$category` |
| SLA Compliance | SLA adherence | Postgres | `$tenant` |
| Backup History | Forensic search | Postgres | `$tenant`, `$server`, `$result_status` |

Shared conventions (status regex, units, thresholds, color, link patterns) are applied
consistently but not centralized — Grafana provisioned JSON has no include mechanism,
so consistency is enforced by the implementation plan and a review checklist, and
guarded by the existing dashboard JSON validity checks.

## Data flow (drill-down)

```
Overview ──(server, category)──▶ Backup Report ──(tenant)──▶ Backup History
   │                                                              ▲
   └── SLA Compliance ──(tenant, asset)───────────────────────────┘
```

## Error / edge handling

- All ratio queries keep `clamp_min(denominator, 1)` to avoid divide-by-zero.
- Stats use `or vector(0)` where an empty result should read as zero, not "No data".
- Sparse `ppdm_asset_last_copy_age_seconds` is preserved (do not add a default 0).
- Postgres panels degrade gracefully when a tenant has no rows (KPIs show 0).

## Testing / verification

- **JSON validity:** every dashboard must parse and load under Grafana provisioning
  (the `make demo` stack: mockppdm → exporter → Prometheus → Grafana, plus Postgres
  for the history/compliance boards).
- **Visual smoke test:** load each board in the demo stack; confirm no "No data" on
  panels that should have data, units render, thresholds color correctly, and
  drill-down links carry variables.
- **Query parity:** shared KPIs (success rate, failed-by-category) must return the
  same value on Overview and Backup Report for the same `$server`/`$category`.
- **No regression** to the exporter binary or image — this is dashboard-JSON-only.

## Out of scope

- No changes to exporter Go code, metric names, or label sets.
- No new metrics. (If a desired panel needs a metric that does not exist, it is noted
  and dropped, not added here.)
- No Grafana version upgrade; target features available in the demo-stack Grafana.
