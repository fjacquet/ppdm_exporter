# Protection-rule metrics + dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose PPDM 20.3.0 protection rules as three metrics plus a dashboard row, showing `N/A` when data is unavailable.

**Architecture:** New `Rules` ResourceCollector reads `/api/v3/rules` and `/api/v3/protection-policy-rule-metrics`. A 404 (PPDM < 20.3) is surfaced by the client as a wrapped `ErrNotFound`; the collector skips that endpoint without error. Samples flow through the existing snapshot to Prometheus + OTLP. Panels use `noValue: "N/A"`.

**Tech Stack:** Go 1.27, resty client, Prometheus client_golang, OTel SDK, Grafana dashboard JSON.

**Spec:** `docs/superpowers/specs/2026-11-22-protection-rules-metrics-design.md`

## Global Constraints

- TDD; `make ci` green (Docker-dependent testcontainers tests may fail locally: then run `go vet ./... && go test -short -race ./... && golangci-lint run`).
- Metric names/labels exactly: `ppdm_rule_count{status}`, `ppdm_rule_last_run_age_seconds{rule}`, `ppdm_rule_assigned_assets{policy,rule}`; the loop adds `server`. One ordered label-key set per metric name (ADR-0006).
- 404 ⇒ no samples + no error (`ppdm_collector_up` stays 1); other errors propagate. Retry excludes 4xx (unchanged).
- Rule never run (`lastRunAt` empty) ⇒ no age sample.
- No inline lint/semgrep suppressions. Exporter stays read-only (GET only).
- New dashboard panels: `fieldConfig.defaults.noValue: "N/A"`; existing panels untouched.

## Review Focus

- Server returns 404 on both endpoints: `collector_up` stays 1, zero rule samples, no error.
- One endpoint 404s, the other returns data: data still emitted.
- 500 / network error still yields an error (`collector_up=0`), not N/A.
- `lastRunAt` empty or unparsable: no age sample, other samples unaffected.
- Zero rules returned (empty `content`): `ppdm_rule_count` emits nothing, not a 0 series.
- `ErrNotFound` survives `GetAll` wrapping (`errors.Is` true).

## File Structure

- Modify `internal/ppdmclient/server.go` (+ `mock.go`): `ErrNotFound`.
- Create `internal/ppdm/rules.go`, `internal/ppdm/rules_test.go`, `internal/ppdm/testdata/rules.json`, `internal/ppdm/testdata/protection-policy-rule-metrics.json`.
- Modify `internal/ppdm/resource.go` (register), `internal/ppdm/spec_conformance_test.go` (2 rows), `internal/ppdm/labels_test.go` (new paths).
- Modify `cmd/mockppdm/main.go` + fixtures; `grafana/dashboards/ppdm-overview.json`; `docs/metrics.md`; create `docs/adr/0017-unavailable-data-is-na.md` + index row.

### Task 1: `ErrNotFound` in the client

**Files:** Modify `internal/ppdmclient/server.go:99-101`, `internal/ppdmclient/mock.go`; Test `internal/ppdmclient/server_test.go`, `paginate_test.go`.

**Interfaces:**
- Produces: `var ErrNotFound = errors.New("not found")` in package `ppdmclient`; `ServerClient.Get` returns `fmt.Errorf("GET %s: status 404: %w", path, ErrNotFound)` on 404 (other codes unchanged); `func (m *Mock) SetNotFoundPrefix(prefix string)` — `Mock.Get` for a path with that prefix returns an error wrapping `ErrNotFound`.

- [ ] **Step 1: Failing tests.** `TestGetNotFoundIsErrNotFound` (httptest server answering 404; assert `errors.Is(err, ErrNotFound)` and a 500 case asserts `!errors.Is`); `TestGetAllPropagatesErrNotFound` (Mock with `SetNotFoundPrefix("/x")`, `GetAll[struct{}](..., "/x", 10)` ⇒ `errors.Is(err, ErrNotFound)`).
- [ ] **Step 2: Run** `go test ./internal/ppdmclient -run 'NotFound'` — Expected: FAIL (undefined).
- [ ] **Step 3: Implement** the sentinel, the 404 branch in `Get`, `SetNotFoundPrefix` (store prefixes; check before normal prefixes), and make `GetAll` wrap with `%w` if it currently flattens errors.
- [ ] **Step 4: Run** `go test ./internal/ppdmclient` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(ppdmclient): ErrNotFound sentinel for 404 responses`.

### Task 2: `Rules` collector

**Files:** Create `internal/ppdm/rules.go`, `internal/ppdm/rules_test.go`, `internal/ppdm/testdata/rules.json`, `internal/ppdm/testdata/protection-policy-rule-metrics.json`; Modify `internal/ppdm/resource.go`, `internal/ppdm/labels_test.go`, `internal/ppdm/spec_conformance_test.go`.

**Interfaces:**
- Consumes: `ppdmclient.ErrNotFound`, `Mock.SetNotFoundPrefix`, `ppdmclient.GetAll[T]`.
- Produces: `type Rules struct{ now func() time.Time }` implementing `ResourceCollector` (`Name()` returns `"rules"`); unexported `rule{Name, Status, LastRunAt string}` (json `name`,`status`,`lastRunAt`) and `ruleMetric{ProtectionPolicyName string; AssignedAssetCount float64; Rule struct{Name string}}` (json `protectionPolicyName`,`assignedAssetCount`,`rule.name`); `Registry(...)` appends `Rules{}`; two rows added to `conformanceRows` (21202, `/api/v3/rules` and `/api/v3/protection-policy-rule-metrics`).

- [ ] **Step 1: Failing tests** in `rules_test.go` with fixtures (3 rules: ACTIVE with lastRunAt, ACTIVE with empty lastRunAt, CREATING; 2 metric rows): 
  - `TestRulesCollect`: fixed `now`; assert `ppdm_rule_count{status=ACTIVE}==2`, `{CREATING}==1`; exactly one `ppdm_rule_last_run_age_seconds` sample (rule with lastRunAt) with the expected seconds; `ppdm_rule_assigned_assets{policy,rule}` values.
  - `TestRulesNotFoundIsSilent`: both endpoints `SetNotFoundPrefix` ⇒ `err == nil`, `len(samples)==0`.
  - `TestRulesOneEndpointNotFound`: only `/api/v3/rules` 404 ⇒ metric samples still returned.
  - `TestRulesErrorPropagates`: Mock with no registration (plain error) ⇒ `err != nil`.
  - `TestRulesEmptyEmitsNoCount`: empty `content` ⇒ no `ppdm_rule_count` sample.
  - Extend `TestLabelKeySetConsistentPerMetric` to register the two new paths/fixtures.
  - Run the ppdm conformance test after adding the two rows (it must pass; the structs must exist first, so add rows in step 3).
- [ ] **Step 2: Run** `go test ./internal/ppdm -run Rules` — Expected: FAIL (undefined `Rules`).
- [ ] **Step 3: Implement** `func (r Rules) Collect(ctx, c) ([]Sample, error)`: fetch each endpoint with `GetAll` (page size 500); on `errors.Is(err, ppdmclient.ErrNotFound)` skip that endpoint; any other error returns. Rule count = count by `status`; age = `now - lastRunAt` (RFC3339) only when parseable; assigned assets per row. `now` defaults to `time.Now` as in `Assets.clock()`. Register in `Registry`; add the two conformance rows.
- [ ] **Step 4: Run** `go test -short -race ./internal/ppdm` — Expected: PASS.
- [ ] **Step 5: Export-path test** `TestRulesViaPrometheusAndOTLP` (pattern of `otlp_test.go` / `prometheus_test.go`): store a snapshot with the three metrics; assert each name present in the Prometheus registry and in an OTLP `ManualReader`.
- [ ] **Step 6: Commit** `feat(ppdm): protection rule metrics (20.3.0)`.

### Task 3: Demo mock, dashboard, docs, ADR

**Files:** Modify `cmd/mockppdm/main.go` (+ `cmd/mockppdm/fixtures/rules.json`, `protection-policy-rule-metrics.json`), `grafana/dashboards/ppdm-overview.json`, `docs/metrics.md`, `docs/adr/index.md`; Create `docs/adr/0017-unavailable-data-is-na.md`.

**Interfaces:** Consumes metric names from Task 2.

- [ ] **Step 1: Failing check.** Add a test in `cmd/mockppdm` (or the existing mock test file) asserting `routes` contains `/api/v3/rules` and `/api/v3/protection-policy-rule-metrics`; and a Go test (e.g. `internal/ppdm/dashboard_test.go`) that loads `grafana/dashboards/ppdm-overview.json` and asserts every panel whose targets reference `ppdm_rule_` has `fieldConfig.defaults.noValue == "N/A"` and that at least three such panels exist. Run: Expected FAIL.
- [ ] **Step 2: Implement** fixtures + routes (fixtures mirror `testdata`); dashboard: new row id 500 "Protection rules" below row 400 (`gridPos.y` after the last panel, shift nothing existing), panels ids 51–53: stat `sum by (status) (ppdm_rule_count{server=~"$server"})`, table `max by (rule) (ppdm_rule_last_run_age_seconds{server=~"$server"})` (unit `s`), bargauge `sum by (rule) (ppdm_rule_assigned_assets{server=~"$server"})`; all `datasource` like neighbors, `noValue:"N/A"`. If other copies of `ppdm-overview.json` exist (grep `charts/`, `deploy/`, `docs/`), update them identically.
- [ ] **Step 3: Docs.** Add three rows to `docs/metrics.md` (name, labels, gauge, help, "absent on PPDM < 20.3 / never-run rules ⇒ N/A"); write ADR-0017 (context: honest absence; decision: no series + `noValue`, 404 ⇒ silent skip, real errors stay `collector_up=0`; consequences: existing panels still show 0/No data — follow-up); add index row.
- [ ] **Step 4: Verify** `go vet ./... && go test -short -race ./... && golangci-lint run`, `jq empty grafana/dashboards/ppdm-overview.json`. Expected: all green.
- [ ] **Step 5: Commit** `feat: protection rules row, demo fixtures, docs (ADR-0017)`.
