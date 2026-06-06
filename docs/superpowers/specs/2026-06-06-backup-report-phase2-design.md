# Backup Assurance Reporter — Phase 2: SLA compliance engine

**Date:** 2026-06-06
**Status:** Approved (brainstorming)
**Builds on:** Phase 1 (`docs/superpowers/specs/2026-06-05-backup-report-phase1-design.md`) — the
durable history store (`backup_jobs`, `copies`, `assets`, `protection_policies`, `capture_runs`).
**Branch:** `feat/backup-report-phase2` (off `feat/backup-report-phase1`).

## Goal

Turn the captured history into **per-asset SLA compliance verdicts** — does each asset meet
its backup-frequency (RPO), retention, and copy-count targets — computed **on-demand** from a
SQL view, with targets **derived from each PPDM protection policy and overridable in config**.
Grafana shows live compliance; Phase 3's report queries the same view.

This is **assurance**, not forensic audit (no signing/WORM). Out of scope: framework badging
(Phase 3 presentation), immutable/offsite checks, and any stored verdict-history table.

## Decisions (this brainstorm)

1. **Hybrid SLA targets** — derive defaults from PPDM `protection_policies.objectives`, override per
   tenant / asset-type / policy-name in config.
2. **Rule set (v1)** — RPO/last-success, retention-met, minimum-copies. (Immutable/offsite deferred.)
3. **On-demand verdicts** — a SQL `compliance` view computes pass/fail live; no stored verdict table.
4. Targets are the only materialized state (`sla_targets`), refreshed each cycle — targets, not verdicts.

## Architecture

Two pieces, splitting the hard part (resolving targets, in Go) from the verdict (a live SQL view):

```
capture cycle ──> resolve SLA targets (Go) ──> sla_targets table
   (Phase 1)        parse objectives + apply config overrides
                                                      │
assets / backup_jobs / copies ──┐                     │
                                ▼                     ▼
                         compliance  (SQL view: assets ⋈ sla_targets, rules computed live)
                                ▼
                         Grafana "Compliance" dashboard + Phase 3 report
```

### 1. Target resolution (Go — `internal/report/sla.go`)

After each capture cycle (and on config reload), for every captured protection policy compute an
effective target and upsert into `sla_targets`:

- **Derive** from `protection_policies.objectives` (provisional shape — an array of stage objects
  carrying `schedule.interval` and `retention.interval` as ISO-8601 durations, e.g. `PT24H`, `P30D`).
  A small ISO-8601 duration helper parses the `PnDTnHnMnS` / `PnW` subset PPDM uses (months≈30d,
  years≈365d are explicitly approximate and documented). Unparseable → fall back to config defaults.
- **Override** by the most specific matching config rule (tenant + asset-type + policy-name, most
  specific wins), producing `rpo_seconds`, `retention_days`, `min_copies`, and a `source`
  (`policy` | `override` | `default`).

`sla_targets` is keyed by `(tenant, policy_name)` (the scope assets resolve through), with columns
`rpo_seconds, retention_days, min_copies, grace_seconds, source`. `grace_seconds` is resolved from
`compliance.grace` so the view never hard-codes it. A per-tenant **default** row is written with
`policy_name = ''` (source `default`) from `compliance.defaults`; the view falls back to it for any
asset whose `policy_name` has no specific target. The table is small and idempotently upserted —
*targets*, not verdicts, so on-demand evaluation is preserved.

### 2. Compliance view (SQL — in `migrations.sql`)

A view `compliance` joins each asset to its target — `LEFT JOIN sla_targets t ON t.tenant=assets.tenant
AND t.policy_name = assets.policy_name`, then `COALESCE` to the tenant default row
(`t.policy_name=''`) — and computes the three rules live using the target's own `grace_seconds`:

- **rpo_ok** — `EXISTS` a `backup_jobs` row for the asset with `result_status IN ('SUCCESS','OK')`
  and `created_at >= now() - make_interval(secs => rpo_seconds + grace_seconds)`; OR
  `assets.last_available_copy_time` within that window (covers assets whose jobs aged out of retention).
- **retention_ok** — the asset's newest `copies` row has
  `EXTRACT(EPOCH FROM (retention_time - create_time)) >= retention_days * 86400`.
- **copies_ok** — `count(copies for asset) >= min_copies`.
- **compliant** = `rpo_ok AND retention_ok AND copies_ok`; a `reasons` text aggregates failing rules.

Columns: `tenant, server, asset_id, asset_name, asset_type, policy_name, rpo_ok, retention_ok,
copies_ok, compliant, reasons, rpo_seconds, retention_days, min_copies`.

> The view is read-only and recomputes on every query (on-demand). `grace_seconds` comes from the
> joined target row (resolved from `compliance.grace`), so the SQL hard-codes nothing.

### Config additions (`internal/config/report.go`)

```yaml
compliance:
  grace: "4h"                                  # lateness tolerance on RPO
  defaults: {rpoHours: 24, retentionDays: 30, minCopies: 2}
  overrides:
    - {tenant: acme-corp, assetType: VMWARE_VIRTUAL_MACHINE, policyName: "", rpoHours: 12, minCopies: 3}
```

Empty selector fields match any value; the most specific matching override wins.

## Data flow / cadence

The capture loop (`RunOnce`) gains a post-capture step: `ResolveTargets(ctx, tenant, cfg)` upserts
`sla_targets` from the just-captured policies + config. The `compliance` view then reflects current
data on any query. `--once` resolves targets once; the looped mode refreshes them each cycle.

## Error handling

- A policy with no parseable objective falls back to config defaults (logged at debug, not an error).
- Target resolution failure for one server is logged and recorded; it does not block capture or other
  servers (reuses the Phase 1 per-server degradation).
- The view tolerates missing targets: assets with no matching `sla_targets` row use a synthesized
  tenant-default row (from `compliance.defaults`, also written to `sla_targets` as a `default` source).

## Testing (TDD)

- **ISO-8601 helper** — unit tests for `PT24H`, `P30D`, `P1W`, `PT12H30M`, `P1M` (≈30d), invalid → error.
- **Target resolution** — table tests: derive-from-objectives, override precedence (tenant+type+policy),
  default fallback; assert `sla_targets` rows (testcontainers Postgres).
- **Compliance view** — seed assets/jobs/copies + targets, assert per-rule and overall verdicts for:
  compliant asset, overdue (rpo fail), short-retention (retention fail), too-few-copies (copies fail),
  and an asset with no target (uses default). Verify `reasons` text.
- `make ci` parity (gofmt/vet/golangci-lint/test-race/govulncheck); semgrep clean.

## Demo / verification

The Grafana stack gains a **Compliance** dashboard over the `compliance` view: per-tenant compliant %
(stat/gauge), a table of non-compliant assets with reasons, and a breakdown by failing rule. The
mock fixtures already provide a Gold-VM policy (`PT24H`/`P30D`) and assets/copies, so after a capture
cycle the dashboard shows live verdicts. End-to-end check: `SELECT compliant, reasons FROM compliance`
returns a row per asset with correct pass/fail.

## Out of scope (Phase 2)

Framework badging (3-2-1-1-0 / ISO / SOC2 control mapping — Phase 3 presentation), immutable/offsite
copy checks, per-tenant configurable grace, stored verdict history, and the branded report (Phase 3).
