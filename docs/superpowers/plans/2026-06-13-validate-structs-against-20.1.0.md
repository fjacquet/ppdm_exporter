# Validate PPDM structs against 20.1.0 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct every deserialization struct field that diverges from the authoritative PPDM 20.1.0 OpenAPI specs, fixing three silent-data-loss bugs and retiring confirmed `provisional` tags.

**Architecture:** Two independent binaries share PPDM shapes. Part A fixes the exporter collectors (`internal/ppdm`); Part B fixes the report capture DTOs (`internal/report`). Each fix is TDD: update the single fixture to the real 20.1.0 shape → red → correct the struct/collector → green. Findings and decisions are recorded in `docs/adr/0010-20.1.0-api-validation.md`; specs are vendored at `docs/swagger/`.

**Tech Stack:** Go, `ppdmclient.Mock` (collectors), `testcontainers-go`/`pgx` (report store), Prometheus + OTLP readers.

**Decisions (from brainstorming):** D1 health `component` ← `healthEntityRef.categories[0]`; D2 copies via `GET /api/v2/latest-copies`; D3 mtree used = total − available, drop logical metric; D4 drop `Copy.policyName`/`expirationTime`, keep their columns (NULL).

---

## Part A — Exporter collectors (`internal/ppdm`)

### Task A1: Activities — fix timestamps + filter, retire confirmed tags

**Files:**
- Modify: `internal/ppdm/activities.go`
- Modify: `internal/ppdm/testdata/activities.json`
- Test: `internal/ppdm/activities_test.go`

20.1.0 `Activity` has `createTime`/`endTime` (no `startedAt`/`completedAt`); the list filter field is `createTime`. `duration` exists but its unit is unconfirmed, so duration stays computed from `endTime − createTime` (➕ `Activity.duration` deferred — see ADR-0010).

- [ ] **Step 1: Update the fixture to the 20.1.0 shape**

Replace `internal/ppdm/testdata/activities.json` content with (rename `startedAt`→`createTime`, `completedAt`→`endTime`):

```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"id":"act-1","name":"Protecting VM - vm-app01","category":"PROTECT","subcategory":"AD_HOC","state":"COMPLETED","createTime":"2026-06-05T01:00:00Z","endTime":"2026-06-05T01:04:12Z","result":{"status":"SUCCESS","bytesTransferred":1048576},"protectionPolicy":{"name":"Gold-VM"},"asset":{"name":"vm-app01"}},
    {"id":"act-2","name":"Protecting DB - sql-db1","category":"PROTECT","subcategory":"SCHEDULED","state":"COMPLETED","createTime":"2026-06-05T02:00:00Z","endTime":"2026-06-05T02:00:08Z","result":{"status":"FAILED","bytesTransferred":0},"protectionPolicy":{"name":"SQL-Daily"},"asset":{"name":"sql-db1"}},
    {"id":"act-3","name":"Restore - nas01","category":"RESTORE","subcategory":"FLR","state":"RUNNING","createTime":"2026-06-05T03:00:00Z","endTime":null,"result":{"status":"OK","bytesTransferred":524288},"protectionPolicy":{"name":""},"asset":{"name":"nas01"}}
  ]
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ppdm/ -run TestActivities -v`
Expected: FAIL — `act-1 duration = 0, want 252` (struct still reads `startedAt`/`completedAt`, now absent).

- [ ] **Step 3: Fix the struct and collector**

In `internal/ppdm/activities.go`, replace the struct (lines 11-33) and update the comment block above it:

```go
// activity is the shape of one /api/v2/activities content item, validated against the
// 20.1.0 Activity schema (docs/swagger/9765, ADR-0010). result.bytesTransferred is
// cumulative + summable (PDF-confirmed on PROTECT activities).
type activity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	State       string `json:"state"`
	CreateTime  string `json:"createTime"` // RFC3339
	EndTime     string `json:"endTime"`    // RFC3339 or "" (running)
	Result      struct {
		Status           string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
	ProtectionPolicy struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"`
	Asset struct {
		Name string `json:"name"`
	} `json:"asset"`
}
```

Update the filter (line 57) from `createdAt` to `createTime`:

```go
	path := "/api/v2/activities?filter=" + url.QueryEscape(`createTime ge "`+since+`"`)
```

Replace `jobDuration` (lines 112-123) to use the renamed fields:

```go
// jobDuration returns endTime-createTime in seconds when both timestamps parse.
func jobDuration(act activity) (float64, bool) {
	if act.CreateTime == "" || act.EndTime == "" {
		return 0, false
	}
	start, err1 := time.Parse(time.RFC3339, act.CreateTime)
	end, err2 := time.Parse(time.RFC3339, act.EndTime)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return end.Sub(start).Seconds(), true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/ppdm/ -run TestActivities -v`
Expected: PASS (act-1 duration 252; act-3 running has no duration series).

- [ ] **Step 5: Commit**

```bash
git add internal/ppdm/activities.go internal/ppdm/testdata/activities.json
git commit -m "fix(ppdm): activity uses createTime/endTime per 20.1.0 (ADR-0010)"
```

### Task A2: Assets — retire confirmed provisional tags

**Files:**
- Modify: `internal/ppdm/assets.go`

All four `asset` fields are confirmed in 20.1.0 `Asset` (ADR-0010); only the comments change.

- [ ] **Step 1: Run the existing test to confirm green baseline**

Run: `go test ./internal/ppdm/ -run TestAssets -v`
Expected: PASS.

- [ ] **Step 2: Retire the provisional comments**

In `internal/ppdm/assets.go` replace the struct (lines 10-15):

```go
// asset is one /api/v2/assets content item, validated against the 20.1.0 Asset schema (ADR-0010).
type asset struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ProtectionStatus      string `json:"protectionStatus"`
	LastAvailableCopyTime string `json:"lastAvailableCopyTime"` // RFC3339 or "" (null)
}
```

- [ ] **Step 3: Run the test to verify it still passes**

Run: `go test ./internal/ppdm/ -run TestAssets -v`
Expected: PASS (no behavior change).

- [ ] **Step 4: Commit**

```bash
git add internal/ppdm/assets.go
git commit -m "docs(ppdm): retire confirmed provisional tags on asset (ADR-0010)"
```

### Task A3: Capacity — nested storage-system fields, derive mtree used, drop logical

**Files:**
- Modify: `internal/ppdm/capacity.go`
- Modify: `internal/ppdm/testdata/datadomain-mtrees.json`
- Modify: `internal/ppdm/testdata/storage-systems.json`
- Test: `internal/ppdm/capacity_test.go`

20.1.0 `DataDomainMTree` has `totalCapacityInBytes`/`availableCapacityInBytes` (no used/logical); `StorageSystem` capacity is nested under `details.dataDomain.{totalSize,totalUsed}`.

- [ ] **Step 1: Check the logical-used metric has no other consumers**

Run: `grep -rn "logical_used\|ppdm_storage_unit_logical" internal/ docs/ deploy/ 2>/dev/null`
Expected: only `capacity.go`, `capacity_test.go`, and possibly a dashboard JSON. If a dashboard references it, note it for removal in the commit; the metric is being dropped (D3).

- [ ] **Step 2: Update the mtree fixture**

Replace `internal/ppdm/testdata/datadomain-mtrees.json`:

```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"name":"/data/col1/su-policy-a","totalCapacityInBytes":3220957036544,"availableCapacityInBytes":2878433394688}
  ]
}
```

(used = 3220957036544 − 2878433394688 = 342523641856, preserving the prior expected value.)

- [ ] **Step 3: Update the storage-system fixture**

Replace `internal/ppdm/testdata/storage-systems.json`:

```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"name":"ddve-01","type":"DATA_DOMAIN_SYSTEM","details":{"dataDomain":{"totalSize":3220957036544,"totalUsed":342523641856}}}
  ]
}
```

- [ ] **Step 4: Update the test expectations**

In `internal/ppdm/capacity_test.go` replace the `want` map (remove the logical line):

```go
	want := map[string]float64{
		"ppdm_storage_unit_physical_capacity_bytes|/data/col1/su-policy-a|": 3220957036544,
		"ppdm_storage_unit_physical_used_bytes|/data/col1/su-policy-a|":     342523641856,
		"ppdm_storage_system_total_bytes||ddve-01":                          3220957036544,
		"ppdm_storage_system_used_bytes||ddve-01":                           342523641856,
	}
```

Add an assertion that the logical metric is gone, after the `want` loop:

```go
	if _, ok := seen["ppdm_storage_unit_logical_used_bytes|/data/col1/su-policy-a|"]; ok {
		t.Error("ppdm_storage_unit_logical_used_bytes should no longer be emitted")
	}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `go test ./internal/ppdm/ -run TestCapacity -v`
Expected: FAIL — capacity values 0 (struct reads old field names) / logical still emitted.

- [ ] **Step 6: Fix the structs and collector**

Replace `internal/ppdm/capacity.go` lines 9-57 with:

```go
// mtree is one /api/v2/datadomain-mtrees content item (20.1.0 DataDomainMTree, ADR-0010).
// 20.1.0 exposes total + available; used is derived. No logical-used field exists.
type mtree struct {
	Name      string  `json:"name"`
	TotalCap  float64 `json:"totalCapacityInBytes"`
	Available float64 `json:"availableCapacityInBytes"`
}

// storageSystem is one /api/v2/storage-systems content item (20.1.0 StorageSystem, ADR-0010).
// DataDomain capacity is nested under details.dataDomain.
type storageSystem struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Details struct {
		DataDomain struct {
			TotalSize float64 `json:"totalSize"`
			TotalUsed float64 `json:"totalUsed"`
		} `json:"dataDomain"`
	} `json:"details"`
}

// Capacity collects storage-unit (MTree) and storage-system capacity in bytes.
type Capacity struct{}

func (Capacity) Name() string { return "capacity" }

func (Capacity) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	mtrees, err := ppdmclient.GetAll[mtree](ctx, c, "/api/v2/datadomain-mtrees", 500)
	if err != nil {
		return nil, err
	}
	systems, err := ppdmclient.GetAll[storageSystem](ctx, c, "/api/v2/storage-systems", 500)
	if err != nil {
		return nil, err
	}
	var out []Sample
	for _, m := range mtrees {
		su := []Label{{Key: "storage_unit", Value: m.Name}}
		out = append(out,
			Sample{Name: "ppdm_storage_unit_physical_capacity_bytes", Value: m.TotalCap, Labels: su},
			Sample{Name: "ppdm_storage_unit_physical_used_bytes", Value: m.TotalCap - m.Available, Labels: su},
		)
	}
	for _, s := range systems {
		sl := []Label{{Key: "storage_system", Value: s.Name}, {Key: "type", Value: s.Type}}
		out = append(out,
			Sample{Name: "ppdm_storage_system_total_bytes", Value: s.Details.DataDomain.TotalSize, Labels: sl},
			Sample{Name: "ppdm_storage_system_used_bytes", Value: s.Details.DataDomain.TotalUsed, Labels: sl},
		)
	}
	return out, nil
}
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `go test ./internal/ppdm/ -run TestCapacity -v`
Expected: PASS.

- [ ] **Step 8: Check the unchecked-collector metric list**

Run: `grep -n "logical_used" internal/ppdm/prometheus.go internal/ppdm/otlp.go internal/ppdm/labels_test.go`
If `ppdm_storage_unit_logical_used_bytes` is registered/listed, remove those references too, then re-run `go test ./internal/ppdm/`.

- [ ] **Step 9: Commit**

```bash
git add internal/ppdm/capacity.go internal/ppdm/testdata/datadomain-mtrees.json internal/ppdm/testdata/storage-systems.json internal/ppdm/capacity_test.go
git commit -m "fix(ppdm): capacity reads 20.1.0 fields; derive mtree used, drop logical (ADR-0010)"
```

### Task A4: Health — read /api/v3/health-results instead of health-entities

**Files:**
- Modify: `internal/ppdm/health.go`
- Rename: `internal/ppdm/testdata/health-entities.json` → `internal/ppdm/testdata/health-results.json`
- Test: `internal/ppdm/health_test.go`

`/api/v3/health-entities` returns rule definitions (no live status). `/api/v3/health-results` returns `HealthResult{status: OK|WARNING|CRITICAL, healthEntityRef:{name, categories[]}}`. The `component` label maps to `categories[0]` (D1).

- [ ] **Step 1: Create the new fixture**

Create `internal/ppdm/testdata/health-results.json`:

```json
{"page":{"number":0,"totalPages":1},"content":[
  {"status":"OK","healthEntityRef":{"name":"ppdm-server","categories":["SYSTEM"]}},
  {"status":"WARNING","healthEntityRef":{"name":"dd-storage","categories":["STORAGE"]}}
]}
```

- [ ] **Step 2: Delete the stale fixture**

```bash
git rm internal/ppdm/testdata/health-entities.json
```

- [ ] **Step 3: Update the test fixture wiring and component assertion**

In `internal/ppdm/health_test.go`, change the prefix/file pair (around line 12) from
`{"/api/v3/health-entities", "testdata/health-entities.json"}` to:

```go
		{"/api/v3/health-results", "testdata/health-results.json"},
```

After the existing `health[...]` assertions, add a component-label check:

```go
	var comp string
	for _, s := range got {
		if s.Name == "ppdm_health_entity_status" && s.LabelValue("entity") == "dd-storage" {
			comp = s.LabelValue("component")
		}
	}
	if comp != "STORAGE" {
		t.Errorf("dd-storage component = %q, want STORAGE", comp)
	}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/ppdm/ -run TestHealth -v`
Expected: FAIL — collector still GETs `/api/v3/health-entities` (Mock has no data) and reads `status`/`component`.

- [ ] **Step 5: Fix the collector**

In `internal/ppdm/health.go` replace the `healthEntity` struct (lines 9-13) with:

```go
// healthResult is one /api/v3/health-results content item (20.1.0 HealthResult, ADR-0010).
// status is the live grade; the entity name + categories come from healthEntityRef.
type healthResult struct {
	Status          string `json:"status"` // OK | WARNING | CRITICAL
	HealthEntityRef struct {
		Name       string   `json:"name"`
		Categories []string `json:"categories"`
	} `json:"healthEntityRef"`
}
```

Replace the entities fetch + loop (lines 29 and 38-46). Change line 29 to:

```go
	results, err := ppdmclient.GetAll[healthResult](ctx, c, "/api/v3/health-results", 500)
```

Replace the `for _, e := range entities` block with:

```go
	for _, r := range results {
		v := 0.0
		if r.Status == "OK" {
			v = 1
		}
		comp := ""
		if len(r.HealthEntityRef.Categories) > 0 {
			comp = r.HealthEntityRef.Categories[0]
		}
		out = append(out, Sample{Name: "ppdm_health_entity_status", Value: v, Labels: []Label{
			{Key: "entity", Value: r.HealthEntityRef.Name}, {Key: "component", Value: comp},
		}})
	}
```

(Also update the `var err error` name `entities` is gone; ensure the variable is `results`.)

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/ppdm/ -run TestHealth -v`
Expected: PASS (ppdm-server=1, dd-storage=0, component=STORAGE).

- [ ] **Step 7: Commit**

```bash
git add internal/ppdm/health.go internal/ppdm/health_test.go internal/ppdm/testdata/health-results.json internal/ppdm/testdata/health-entities.json
git commit -m "fix(ppdm): health reads /api/v3/health-results for live status (ADR-0010)"
```

### Task A5: Collector gate

- [ ] **Step 1: Run the full ppdm package gate**

Run: `make sure` (or `go test ./internal/ppdm/... -race`)
Expected: PASS, no lint/vet errors. The `alert` struct is already confirmed (severity + acknowledgement.acknowledgeState) — no change needed.

---

## Part B — Report capture DTOs (`internal/report`)

### Task B1: Job — createTime/endTime + activities filter

**Files:**
- Modify: `internal/report/models.go`
- Modify: `internal/report/capture.go`
- Modify: `internal/report/store.go`
- Test: `internal/report/capture_test.go`, `internal/report/upsert_test.go`

`Activity` has `createTime`/`endTime`, no `createdAt`/`startedAt`/`completedAt`. `created_at` feeds the RPO SLA, so this is the critical fix. `started_at` has no distinct source → populate from `createTime` (≈ job start).

- [ ] **Step 1: Find the test fixtures/JSON that drive Job capture**

Run: `grep -rn "createdAt\|startedAt\|completedAt\|createTime" internal/report/*_test.go internal/report/testdata 2>/dev/null`
Expected: a list of test inputs using the old field names. These get updated in Step 2.

- [ ] **Step 2: Update Job test inputs to 20.1.0 field names**

In every report test JSON/struct literal found in Step 1, rename `createdAt`→`createTime`, `completedAt`→`endTime`, and remove `startedAt`. (Update the corresponding expected `started_at`/`created_at` row values to equal the `createTime` instant.)

- [ ] **Step 3: Run the report tests to verify failure**

Run: `go test ./internal/report/ -run "Upsert|Capture|SLA" -short -v`
Expected: FAIL — `createdAt`/`completedAt` no longer unmarshal; `created_at` NULL breaks RPO assertions.

- [ ] **Step 4: Fix the Job struct**

In `internal/report/models.go` replace the `Job` timestamp fields (lines 28-30):

```go
	CreateTime  string `json:"createTime"`
	EndTime     string `json:"endTime"`
```

(Remove the `CreatedAt`, `StartedAt`, `CompletedAt` fields. Keep `ID/Category/Subcategory/State/Result/Asset/ProtectionPolicy`; retire the `// provisional` comments on `asset`/`protectionPolicy` — both confirmed as `Resource`.)

- [ ] **Step 5: Fix the activities filter in the capturer**

In `internal/report/capture.go` replace `activitiesPath` (line 133):

```go
	return "/api/v2/activities?filter=" + url.QueryEscape(`createTime ge "`+since.UTC().Format(time.RFC3339)+`"`)
```

- [ ] **Step 6: Fix the UpsertJobs bindings**

In `internal/report/store.go` UpsertJobs, change the timestamp args (line 59-60) so `started_at` and `created_at` both use `createTime` and `completed_at` uses `endTime`:

```go
			j.ProtectionPolicy.Name, ts(j.CreateTime), ts(j.EndTime), int64(j.Result.BytesTransferred),
			ts(j.CreateTime), capturedAt)
```

- [ ] **Step 7: Run the report tests to verify they pass**

Run: `go test ./internal/report/ -run "Upsert|Capture|SLA" -short -v`
Expected: PASS (RPO uses a populated `created_at`).

- [ ] **Step 8: Commit**

```bash
git add internal/report/models.go internal/report/capture.go internal/report/store.go internal/report/*_test.go
git commit -m "fix(report): Job uses createTime/endTime; fixes RPO SLA (ADR-0010)"
```

### Task B2: Copy — latest-copies endpoint, retentionLock enum, drop orphan fields

**Files:**
- Modify: `internal/report/models.go`
- Modify: `internal/report/capture.go`
- Modify: `internal/report/store.go`
- Test: `internal/report/*_test.go`

`GET /api/v2/copies` is gone (D2 → `/api/v2/latest-copies`). `Copy.retentionLock` is a string enum, not bool. `policyName`/`expirationTime` have no source (D4 → drop fields, keep columns NULL).

- [ ] **Step 1: Update Copy test inputs**

In the report test JSON/literals for copies: remove `policyName` and `expirationTime`; change `retentionLock` from `true`/`false` to a string enum value (`"ALL_COPIES_LOCKED"` where the test expects immutable, `"ALL_COPIES_UNLOCKED"` otherwise). Update any expected `policy_name`/`expiration_time` row values to empty/NULL, and `has_immutable` expectations to match the enum.

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/report/ -run "Upsert|Copies|321|SLA" -short -v`
Expected: FAIL — `retentionLock` type mismatch / removed fields.

- [ ] **Step 3: Fix the Copy struct**

In `internal/report/models.go` replace the `Copy` struct (lines 51-64):

```go
// Copy is one /api/v2/latest-copies record (20.1.0 Copy, ADR-0010). policyName and
// expirationTime have no 20.1.0 source and are not captured (columns remain, NULL).
type Copy struct {
	ID              string  `json:"id"`
	AssetID         string  `json:"assetId"`
	CopyType        string  `json:"copyType"`
	CreateTime      string  `json:"createTime"`
	RetentionTime   string  `json:"retentionTime"`
	RetentionLock   string  `json:"retentionLock"` // enum ALL_COPIES_UNLOCKED|ALL_COPIES_LOCKED|PARTIAL_COPIES_LOCKED
	StorageSystemID string  `json:"storageSystemId"`
	Location        string  `json:"location"`
	Size            float64 `json:"size"`
}

// locked reports whether any child copy is retention-locked (for the has_immutable rollup).
func (c Copy) locked() bool {
	return c.RetentionLock == "ALL_COPIES_LOCKED" || c.RetentionLock == "PARTIAL_COPIES_LOCKED"
}
```

- [ ] **Step 4: Fix the copies endpoint**

In `internal/report/capture.go` replace `copiesPath` (line 137):

```go
	return "/api/v2/latest-copies?filter=" + url.QueryEscape(`createTime ge "`+since.UTC().Format(time.RFC3339)+`"`)
```

- [ ] **Step 5: Fix the UpsertCopies bindings**

In `internal/report/store.go` UpsertCopies, drop `policyName`/`expirationTime` sources and use `locked()`. Replace the args (lines 76-78):

```go
			c.ID, tenant, server, c.AssetID, "", c.CopyType, ts(c.CreateTime),
			ts(""), ts(c.RetentionTime), c.locked(), c.StorageSystemID,
			c.Location, int64(c.Size), capturedAt)
```

(`""` for `policy_name`; `ts("")` yields NULL `expiration_time`. Column list/order in the SQL is unchanged.)

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/report/ -run "Upsert|Copies|321|SLA" -short -v`
Expected: PASS (`has_immutable` driven by the enum).

- [ ] **Step 7: Commit**

```bash
git add internal/report/models.go internal/report/capture.go internal/report/store.go internal/report/*_test.go
git commit -m "fix(report): copies via latest-copies; retentionLock enum; drop orphan fields (ADR-0010)"
```

### Task B3: report Asset & Policy — retire confirmed tags

**Files:**
- Modify: `internal/report/models.go`

`Asset` (protectionStatus, lastAvailableCopyTime, protectionPolicy.name via `ParentProtectionPolicy`) and `Policy` (id/name/objectives) are confirmed (ADR-0010).

- [ ] **Step 1: Retire the provisional comments**

In `internal/report/models.go`, remove the `// provisional` comments on `Asset.ProtectionStatus`, `Asset.LastAvailableCopyTime`, `Asset.protectionPolicy`, and `Policy.Objectives` (keep the "stored as jsonb" note on Objectives). No field/type changes.

- [ ] **Step 2: Run the report tests**

Run: `go test ./internal/report/ -short`
Expected: PASS (comment-only change).

- [ ] **Step 3: Commit**

```bash
git add internal/report/models.go
git commit -m "docs(report): retire confirmed provisional tags on Asset/Policy (ADR-0010)"
```

---

## Task C: Final verification

- [ ] **Step 1: Confirm only genuinely-unverified shapes remain tagged**

Run: `grep -rn provisional internal/`
Expected: no `provisional` tags remain in `internal/ppdm` or `internal/report` collector/DTO files for fields validated here. Any remaining hits should be intentional (document in ADR-0010 if so).

- [ ] **Step 2: Run the full CI gate**

Run: `make ci`
Expected: PASS — gofmt, vet, golangci-lint, `go test -race`, govulncheck, build all green.

- [ ] **Step 3: Live-appliance smoke (optional, if an appliance is reachable)**

Run: `./bin/ppdm_exporter --config config.yaml --once --debug --trace 2>trace.log | sort > samples.txt`
Inspect `samples.txt` for non-zero `ppdm_storage_system_*`, `ppdm_health_entity_status`, and `ppdm_activity_*` series; confirm `trace.log` shows 200s on `/api/v3/health-results` and `/api/v2/latest-copies`.

- [ ] **Step 4: Commit any doc/dashboard follow-ups**

If a Grafana dashboard referenced `ppdm_storage_unit_logical_used_bytes`, update it and commit:

```bash
git add deploy/ docs/
git commit -m "docs: drop logical-used panel; align dashboards with 20.1.0 metrics (ADR-0010)"
```

---

## Notes for the implementer

- **One fixture per shape** (ADR-0009): each struct fix touches exactly one `testdata` file.
- **Label-key invariant** (ADR-0006): `labels_test.go` gates label sets. Dropping the logical-used metric removes a *name*, not a label key; the health `component` label key is preserved (now sourced from `categories[0]`).
- **Migrations are append-only** idempotent `CREATE IF NOT EXISTS`: D4 keeps the `policy_name`/`expiration_time` columns (now NULL) — no schema edit, lowest risk.
- **`ts(s string)` helper** returns NULL for empty input — relied on for `expiration_time` and any blank timestamp.
- Specs to consult while implementing: `docs/swagger/9765-20.1.0.json` (v2), `docs/swagger/9628-20.1.0.json` (v3); resolve `#/components/schemas/<Name>` with `jq`.
