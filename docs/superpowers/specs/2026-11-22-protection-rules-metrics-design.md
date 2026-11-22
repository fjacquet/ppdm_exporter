# Protection-rule metrics + dashboard (phase 2) — design

## Intent
PPDM 20.3.0 exposes the automatic-assignment rules (`/api/v3/rules`,
`/api/v3/protection-policy-rule-metrics`). Surface them so an operator sees rules that
stopped matching assets or stopped running. User's principle: **when the information is
not available, show `N/A`, never a 0 that looks healthy.**

## Goals
- Three metrics (user-approved):
  - `ppdm_rule_count{status}` — rules per status (`ACTIVE|CREATING|DELETING`), from `/api/v3/rules`.
  - `ppdm_rule_last_run_age_seconds{rule}` — seconds since `lastRunAt`; **no sample** when `lastRunAt` is empty (never run → N/A).
  - `ppdm_rule_assigned_assets{policy,rule}` — `assignedAssetCount`, from `/api/v3/protection-policy-rule-metrics`.
- New collector `Rules` (`internal/ppdm/rules.go`), registered in `Registry`; Prometheus + OTLP via the existing snapshot path.
- **404 handling:** a 404 on a rules endpoint means "PPDM < 20.3": the collector emits no samples for it and returns **no error** (`ppdm_collector_up` stays 1). Any other error stays an error (`collector_up=0`). Implemented as a sentinel `ppdmclient.ErrNotFound` (wrapped, `errors.Is`) returned by `Get` on 404; the two endpoints are handled independently.
- Dashboard `grafana/dashboards/ppdm-overview.json`: new row "Protection rules" — stat (rules by status), table (last run age per rule), bargauge (assets per rule). Every new panel has `fieldConfig.defaults.noValue: "N/A"`. Existing panels untouched.
- Conformance: add the two decode structs to `conformanceRows`.
- Demo: `cmd/mockppdm` serves fixtures for both endpoints so `make demo` shows the row.
- ADR-0017 "Unavailable data is N/A, not zero".

## Non-goals
- `file-selection-rule-metrics`, `rule-execution`, `copy-management`, `tls-config` (POST/config/duplicative).
- Changing existing panels or other collectors to N/A (follow-up if wanted).
- Any write call; exporter stays read-only.

## Approaches
1. **Chosen — absent series + `noValue:"N/A"`, 404 ⇒ silent skip.** Smallest; keeps `collector_up` meaningful.
2. `ppdm_feature_supported` gauge read by panels: more precise, more PromQL per panel — rejected.
3. Probe PPDM version first: fragile — rejected.

## Constraints
- TDD via `ppdmclient.Mock`; assert Prometheus registry and OTLP `ManualReader`.
- Label-key invariant (ADR-0006): one key set per metric name; per-object metrics own names (rule age is separate from rule count).
- Spec fields marked `// provisional` only where unconfirmed; here all are in the 20.3.0 spec, so none.
- No inline lint suppressions; retry still excludes 4xx.
- Per-rule/per-policy cardinality is bounded by rule count (tens).

## Left to the builder
- Exact JSON shape of the nested `rule` object (name field) — read from `21202-20.3.0.json`.
- Panel layout/gridPos and PromQL details; help strings.
- Whether `Mock` needs a way to return 404 (add the smallest helper if so).
