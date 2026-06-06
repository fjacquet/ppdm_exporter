# Backup Reporter Phase 3 — Branded Report + 3-2-1-1-0 Badge — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render per-tenant, current-snapshot backup-assurance reports (HTML + pure-Go PDF) over the Phase 2 `compliance` view, add a computed 3-2-1-1-0 badge, and expose generation via a `report render` CLI subcommand and a read-only `GET /report` HTTP endpoint.

**Architecture:** A new `rule_321110` SQL view (parallel to `compliance`) plus read-only `report.Store` query methods feed a shared `render.ReportData`, rendered two ways (`html/template` + maroto v2 PDF) and exposed two ways (cobra subcommand + an HTTP handler served by the capture process).

**Tech Stack:** Go 1.26, pgx v5, `html/template` (stdlib), maroto v2 (`github.com/johnfercher/maroto/v2`, pure-Go PDF), cobra, testcontainers-go, `net/http`/`httptest`.

Spec: `docs/superpowers/specs/2026-06-06-backup-report-phase3-design.md`.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/report/migrations.sql` (modify) | add `rule_321110` view |
| `internal/report/store.go` (modify) | add `ComplianceRow`, `Rule321Row`, `Summary` types + `ComplianceRows`/`Rule321Rows`/`ReportSummary` read methods |
| `internal/report/store_321_test.go` (create) | testcontainers tests for the view + read methods |
| `internal/config/report.go` (modify) | add `Report` config block (`listen`,`authToken`,`brandName`) + defaults |
| `internal/config/report_test.go` (modify) | config parse/default test |
| `internal/report/render/data.go` (create) | `ReportData` + `Build(ctx, store, tenant, brand, now)` |
| `internal/report/render/html.go` (create) | `RenderHTML` + embedded template/CSS |
| `internal/report/render/report.html.tmpl` (create) | the HTML template |
| `internal/report/render/pdf.go` (create) | `RenderPDF` (maroto v2) |
| `internal/report/render/http.go` (create) | `NewHandler` (GET /report, /healthz, auth, headers) |
| `internal/report/render/*_test.go` (create) | Build/HTML/PDF/HTTP tests |
| `cmd/report/main.go` (modify) | `render` subcommand + serve handler when `report.listen` set |
| `go.mod`/`go.sum` (modify) | add maroto v2 |
| `docs/report.md`, `CHANGELOG.md` (modify) | document Phase 3 |

**Convention reminders (from CLAUDE.md / Phase 2):** parameterized SQL only (`$1..$N`); no inline lint/semgrep suppressions; testcontainers wait on the second "ready to accept connections" log (already in `newTestStore`); run `gofmt` before each commit; commit messages end with the `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer.

---

## Task 1: `rule_321110` view + `Rule321Rows`

**Files:**
- Modify: `internal/report/migrations.sql` (append after the `compliance` view)
- Modify: `internal/report/store.go` (add `Rule321Row` type + `Rule321Rows` method)
- Test: `internal/report/store_321_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/report/store_321_test.go`:

```go
package report

import (
	"context"
	"testing"
)

func TestRule321Rows(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	// a_pass: 3 copies, 2 media, 2 locations, one immutable, no failed job -> full pass.
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,policy_name,updated_at,captured_at)
	      VALUES ('a_pass','acme','s1','full','VMWARE_VIRTUAL_MACHINE','PROTECTED','Gold-VM',now(),now())`)
	exec(`INSERT INTO copies (id,tenant,server,asset_id,storage_system_id,location,retention_lock,create_time,retention_time,captured_at) VALUES
	      ('p1','acme','s1','a_pass','dd-1','site-a',true , now(), now()+interval '31 days', now()),
	      ('p2','acme','s1','a_pass','dd-2','site-b',false, now(), now()+interval '31 days', now()),
	      ('p3','acme','s1','a_pass','dd-2','site-b',false, now(), now()+interval '31 days', now())`)
	// a_fail: 1 copy, 1 media, 1 location, not immutable, plus a FAILED job -> all-fail except none.
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,policy_name,updated_at,captured_at)
	      VALUES ('a_fail','acme','s1','thin','VMWARE_VIRTUAL_MACHINE','PROTECTED','Gold-VM',now(),now())`)
	exec(`INSERT INTO copies (id,tenant,server,asset_id,storage_system_id,location,retention_lock,create_time,retention_time,captured_at) VALUES
	      ('f1','acme','s1','a_fail','dd-1','site-a',false, now(), now()+interval '31 days', now())`)
	exec(`INSERT INTO backup_jobs (id,tenant,server,result_status,asset_id,created_at,captured_at)
	      VALUES ('jf','acme','s1','FAILED','a_fail', now(), now())`)

	rows, err := st.Rule321Rows(ctx, "acme")
	if err != nil {
		t.Fatalf("Rule321Rows: %v", err)
	}
	got := map[string]Rule321Row{}
	for _, r := range rows {
		got[r.AssetID] = r
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	p := got["a_pass"]
	if !(p.CopiesOk && p.MediaOk && p.OffsiteOk && p.ImmutableOk && p.ErrorsOk && p.RulePass) {
		t.Errorf("a_pass = %+v, want all true", p)
	}
	if p.CopiesCount != 3 || p.DistinctMedia != 2 || p.DistinctLocations != 2 {
		t.Errorf("a_pass counts = %+v", p)
	}
	f := got["a_fail"]
	if f.CopiesOk || f.MediaOk || f.OffsiteOk || f.ImmutableOk || f.ErrorsOk || f.RulePass {
		t.Errorf("a_fail = %+v, want all false", f)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRule321Rows ./internal/report/`
Expected: build failure — `undefined: Rule321Row` / `st.Rule321Rows undefined`.

- [ ] **Step 3a: Add the view to `migrations.sql`**

Append to `internal/report/migrations.sql`:

```sql
-- Phase 3: 3-2-1-1-0 backup-rule badge, per asset, computed live (read-only) parallel to
-- compliance. media (distinct storage_system_id) and offsite (distinct location) ride
-- provisional copy fields, so the badge is best-effort and labelled as such in the report.
CREATE OR REPLACE VIEW rule_321110 AS
WITH per_asset AS (
  SELECT a.tenant, a.server, a.id AS asset_id, a.name AS asset_name, a.type AS asset_type,
    (SELECT count(*) FROM copies c WHERE c.asset_id = a.id AND c.server = a.server) AS copies_count,
    (SELECT count(DISTINCT c.storage_system_id) FROM copies c
       WHERE c.asset_id = a.id AND c.server = a.server AND c.storage_system_id <> '') AS distinct_media,
    (SELECT count(DISTINCT c.location) FROM copies c
       WHERE c.asset_id = a.id AND c.server = a.server AND c.location <> '') AS distinct_locations,
    COALESCE((SELECT bool_or(c.retention_lock) FROM copies c
       WHERE c.asset_id = a.id AND c.server = a.server), false) AS has_immutable,
    NOT EXISTS (SELECT 1 FROM backup_jobs j
       WHERE j.asset_id = a.id AND j.server = a.server AND j.result_status = 'FAILED') AS errors_ok
  FROM assets a
)
SELECT tenant, server, asset_id, asset_name, asset_type,
  copies_count, distinct_media, distinct_locations,
  (copies_count >= 3)      AS copies_ok,
  (distinct_media >= 2)    AS media_ok,
  (distinct_locations >= 2) AS offsite_ok,
  has_immutable            AS immutable_ok,
  errors_ok,
  ((copies_count >= 3) AND (distinct_media >= 2) AND (distinct_locations >= 2)
    AND has_immutable AND errors_ok) AS rule_pass
FROM per_asset;
```

- [ ] **Step 3b: Add the type + method to `store.go`**

Add to `internal/report/store.go` (near the SLA read additions):

```go
// Rule321Row is one asset's 3-2-1-1-0 evaluation from the rule_321110 view.
type Rule321Row struct {
	AssetID, AssetName, AssetType                              string
	CopiesOk, MediaOk, OffsiteOk, ImmutableOk, ErrorsOk        bool
	RulePass                                                   bool
	CopiesCount, DistinctMedia, DistinctLocations             int
}

// Rule321Rows returns a tenant's per-asset 3-2-1-1-0 verdicts.
func (s *Store) Rule321Rows(ctx context.Context, tenant string) ([]Rule321Row, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT asset_id, asset_name, asset_type, copies_ok, media_ok, offsite_ok, immutable_ok,
		        errors_ok, rule_pass, copies_count, distinct_media, distinct_locations
		 FROM rule_321110 WHERE tenant=$1 ORDER BY asset_name`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule321Row
	for rows.Next() {
		var r Rule321Row
		if err := rows.Scan(&r.AssetID, &r.AssetName, &r.AssetType, &r.CopiesOk, &r.MediaOk,
			&r.OffsiteOk, &r.ImmutableOk, &r.ErrorsOk, &r.RulePass,
			&r.CopiesCount, &r.DistinctMedia, &r.DistinctLocations); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/report/store.go internal/report/store_321_test.go && go test -run TestRule321Rows ./internal/report/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/report/
git add internal/report/migrations.sql internal/report/store.go internal/report/store_321_test.go
git commit -m "$(printf 'feat(report): rule_321110 view + Rule321Rows\n\nPer-asset 3-2-1-1-0 backup-rule verdict (3 copies, 2 media, 1 offsite, 1\nimmutable, 0 errors) computed live, parallel to the compliance view. media\nand offsite ride provisional copy fields (documented). Store gains Rule321Row\n+ Rule321Rows.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 2: `ComplianceRows` + `ReportSummary`

**Files:**
- Modify: `internal/report/store.go`
- Test: `internal/report/store_321_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/report/store_321_test.go`:

```go
func TestComplianceRowsAndSummary(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	// Default target so the compliance view resolves; one compliant, one copies-failing asset.
	if err := st.UpsertSLATargets(ctx, []SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,last_available_copy_time,policy_name,updated_at,captured_at) VALUES
	      ('ok','acme','s1','ok','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','',now(),now()),
	      ('cop','acme','s1','cop','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','',now(),now())`)
	exec(`INSERT INTO copies (id,tenant,server,asset_id,create_time,retention_time,captured_at) VALUES
	      ('o1','acme','s1','ok', now()-interval '1 hour', now()+interval '31 days', now()),
	      ('o2','acme','s1','ok', now()-interval '2 hours', now()+interval '31 days', now()),
	      ('p1','acme','s1','cop', now()-interval '1 hour', now()+interval '31 days', now())`)

	cr, err := st.ComplianceRows(ctx, "acme")
	if err != nil {
		t.Fatalf("ComplianceRows: %v", err)
	}
	if len(cr) != 2 {
		t.Fatalf("compliance rows = %d, want 2", len(cr))
	}
	sum, err := st.ReportSummary(ctx, "acme")
	if err != nil {
		t.Fatalf("ReportSummary: %v", err)
	}
	if sum.TotalAssets != 2 || sum.CompliantAssets != 1 || sum.CopiesFailures != 1 {
		t.Errorf("summary = %+v, want total 2 / compliant 1 / copiesFail 1", sum)
	}
	if sum.BadgePass { // no asset has 3 copies / 2 media -> badge fails
		t.Errorf("badge should be false, got %+v", sum)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestComplianceRowsAndSummary ./internal/report/`
Expected: build failure — `undefined: ComplianceRow` / `st.ComplianceRows undefined` / `st.ReportSummary undefined`.

- [ ] **Step 3: Add types + methods to `store.go`**

```go
// ComplianceRow is one asset's SLA verdict from the compliance view.
type ComplianceRow struct {
	AssetID, AssetName, AssetType, PolicyName string
	RPOOk, RetentionOk, CopiesOk, Compliant   bool
	Reasons                                    string
}

// ComplianceRows returns a tenant's per-asset SLA verdicts (non-compliant first).
func (s *Store) ComplianceRows(ctx context.Context, tenant string) ([]ComplianceRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT asset_id, asset_name, asset_type, policy_name, rpo_ok, retention_ok, copies_ok,
		        compliant, reasons
		 FROM compliance WHERE tenant=$1 ORDER BY compliant, asset_name`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComplianceRow
	for rows.Next() {
		var r ComplianceRow
		if err := rows.Scan(&r.AssetID, &r.AssetName, &r.AssetType, &r.PolicyName,
			&r.RPOOk, &r.RetentionOk, &r.CopiesOk, &r.Compliant, &r.Reasons); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Summary is the report's headline tally for a tenant.
type Summary struct {
	TotalAssets, CompliantAssets               int
	RPOFailures, RetentionFailures, CopiesFailures int
	BadgePass                                  bool // every asset passes 3-2-1-1-0
}

// ReportSummary computes the headline counts for a tenant in one round-trip.
func (s *Store) ReportSummary(ctx context.Context, tenant string) (Summary, error) {
	var sm Summary
	err := s.pool.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM compliance WHERE tenant=$1),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND compliant),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND NOT rpo_ok),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND NOT retention_ok),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND NOT copies_ok),
		 COALESCE((SELECT bool_and(rule_pass) FROM rule_321110 WHERE tenant=$1), false)`,
		tenant).Scan(&sm.TotalAssets, &sm.CompliantAssets, &sm.RPOFailures,
		&sm.RetentionFailures, &sm.CopiesFailures, &sm.BadgePass)
	return sm, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/report/ && go test -run TestComplianceRowsAndSummary ./internal/report/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/store.go internal/report/store_321_test.go
git commit -m "$(printf 'feat(report): ComplianceRows + ReportSummary read methods\n\nTyped, parameterized reads of the compliance view (per-asset verdicts) and a\none-round-trip headline summary (totals, per-rule failures, 3-2-1-1-0 badge).\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 3: `report:` config block

**Files:**
- Modify: `internal/config/report.go`
- Test: `internal/config/report_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/report_test.go`:

```go
func TestLoadReportReportBlock(t *testing.T) {
	t.Setenv("REPORT_TOKEN", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
report:
  listen: "127.0.0.1:9103"
  authToken: "${REPORT_TOKEN}"
  brandName: "Acme Backup Assurance"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Report.Listen != "127.0.0.1:9103" || cfg.Report.AuthToken != "s3cret" ||
		cfg.Report.BrandName != "Acme Backup Assurance" {
		t.Fatalf("report = %+v", cfg.Report)
	}
}

func TestLoadReportReportBrandDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(path, []byte("database: {dsn: x}\nservers:\n  - {name: p, host: h, username: u, password: p}\n"), 0o600)
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Report.BrandName != "Backup Assurance Report" {
		t.Fatalf("default brand = %q", cfg.Report.BrandName)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestLoadReportReport ./internal/config/`
Expected: build failure — `cfg.Report undefined`.

- [ ] **Step 3: Add the struct + defaults to `report.go`**

In `internal/config/report.go`, add the field to `ReportConfig`:

```go
	Compliance Compliance     `yaml:"compliance"`
	Report     ReportOutput   `yaml:"report"`
}

// ReportOutput configures Phase 3 report generation: the optional HTTP endpoint and branding.
type ReportOutput struct {
	Listen    string `yaml:"listen"`    // empty = CLI-only (no HTTP endpoint)
	AuthToken string `yaml:"authToken"` // optional bearer; empty = no auth (localhost posture)
	BrandName string `yaml:"brandName"`
}
```

In `LoadReport`, interpolate the token (next to the existing interpolations) and default the brand. Add after the compliance defaults block:

```go
	token, err := interpolate(cfg.Report.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("report authToken: %w", err)
	}
	cfg.Report.AuthToken = token
	if cfg.Report.BrandName == "" {
		cfg.Report.BrandName = "Backup Assurance Report"
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/config/ && go test -run TestLoadReportReport ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/report.go internal/config/report_test.go
git commit -m "$(printf 'feat(config): report block (listen, authToken, brandName)\n\nOptional HTTP endpoint address, optional bearer token (env-interpolated), and\nreport branding (default \"Backup Assurance Report\"). Empty listen = CLI-only.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 4: `render.ReportData` + `Build`

**Files:**
- Create: `internal/report/render/data.go`
- Test: `internal/report/render/data_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/report/render/data_test.go`:

```go
package render

import (
	"context"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/reporttest"
)

// storeFor spins up a migrated store with one compliant + one copies-failing asset for tenant
// acme, seeded through the store's exported Upsert* methods (no raw SQL / pool access needed).
func storeFor(t *testing.T) *report.Store {
	t.Helper()
	st := reporttest.NewStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertSLATargets(ctx, []report.SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	if err := st.UpsertAssets(ctx, "acme", "s1", []report.Asset{
		{ID: "ok", Name: "ok", Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED", LastAvailableCopyTime: recent},
		{ID: "cop", Name: "cop", Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED", LastAvailableCopyTime: recent},
	}, now); err != nil {
		t.Fatal(err)
	}
	ct := now.Add(-time.Hour).Format(time.RFC3339)
	rt := now.Add(30*24*time.Hour + time.Hour).Format(time.RFC3339) // ~31d retention span
	cp := func(id, asset string) report.Copy {
		return report.Copy{ID: id, AssetID: asset, CreateTime: ct, RetentionTime: rt}
	}
	// asset "ok" gets 2 copies (meets min-copies 2) -> compliant; "cop" gets 1 -> copies fail.
	if err := st.UpsertCopies(ctx, "acme", "s1",
		[]report.Copy{cp("o1", "ok"), cp("o2", "ok"), cp("p1", "cop")}, now); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBuild(t *testing.T) {
	st := storeFor(t)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	d, err := Build(context.Background(), st, "acme", "Acme Co", now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d.Tenant != "acme" || d.BrandName != "Acme Co" || !d.GeneratedAt.Equal(now) {
		t.Fatalf("header = %+v", d)
	}
	if d.Summary.TotalAssets != 2 || d.Summary.CompliantAssets != 1 {
		t.Errorf("summary = %+v", d.Summary)
	}
	if len(d.Compliance) != 2 || len(d.Rule321) != 2 {
		t.Errorf("rows: compliance=%d rule321=%d", len(d.Compliance), len(d.Rule321))
	}
}

func TestBuildNoAssets(t *testing.T) {
	st := reporttest.NewStore(t)
	if _, err := Build(context.Background(), st, "ghost", "B", time.Unix(0, 0)); err == nil {
		t.Fatal("expected error for tenant with no assets")
	}
}
```

- [ ] **Step 2: Create the `reporttest` helper package (so sibling packages can spin up a DB)**

`reporttest` is a separate package that imports `report` (production), so it can be imported by
the test files of sibling packages without an import cycle. It must NOT live inside package
`report` — an internal `report` test file importing a package that imports `report` would cycle,
and putting `import "testing"` in a normal `report` source file would link `testing` into the
production binary. The container setup is duplicated from `store_test.go` (≈20 lines) on purpose.

Create `internal/report/reporttest/reporttest.go`:

```go
// Package reporttest spins up a migrated report.Store backed by a real (throwaway) Postgres via
// testcontainers, for use by tests in sibling packages (e.g. report/render).
package reporttest

import (
	"context"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewStore returns a migrated Store on a throwaway Postgres; skipped under -short. Waits on the
// second "ready to accept connections" log to avoid the init-restart connection-reset flake.
func NewStore(t *testing.T) *report.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres testcontainers in -short mode")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("backup_report"),
		tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	st, err := report.New(ctx, dsn)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/report/render/`
Expected: build failure — `undefined: Build` / `undefined: ReportData`.

- [ ] **Step 4: Implement `data.go`**

Create `internal/report/render/data.go`:

```go
// Package render builds and renders per-tenant backup-assurance reports (HTML + PDF) over the
// report store's compliance and rule_321110 views.
package render

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
)

// ErrNoData is returned by Build when a tenant has no captured assets (→ HTTP 404, distinct from
// a database failure → 500). errUnsupportedFormat is shared by the CLI and HTTP format checks.
var (
	ErrNoData            = errors.New("no captured assets for tenant")
	errUnsupportedFormat = errors.New("unsupported format (want html or pdf)")
)

// ReportData is everything a renderer needs for one tenant's current-snapshot report.
type ReportData struct {
	Tenant      string
	BrandName   string
	GeneratedAt time.Time
	Summary     report.Summary
	Compliance  []report.ComplianceRow
	Rule321     []report.Rule321Row
}

// CompliantPercent is the share of assets meeting every SLA rule, 0..100 (0 when no assets).
func (d ReportData) CompliantPercent() int {
	if d.Summary.TotalAssets == 0 {
		return 0
	}
	return d.Summary.CompliantAssets * 100 / d.Summary.TotalAssets
}

// BadgeText renders the 3-2-1-1-0 verdict as a word.
func (d ReportData) BadgeText() string {
	if d.Summary.BadgePass {
		return "PASS"
	}
	return "FAIL"
}

// Build assembles a tenant's report from the store. now is injected (testable). It errors when
// the tenant has no assets — there is nothing to assure.
func Build(ctx context.Context, st *report.Store, tenant, brand string, now time.Time) (ReportData, error) {
	sum, err := st.ReportSummary(ctx, tenant)
	if err != nil {
		return ReportData{}, fmt.Errorf("summary: %w", err)
	}
	if sum.TotalAssets == 0 {
		return ReportData{}, fmt.Errorf("%w: %q", ErrNoData, tenant)
	}
	comp, err := st.ComplianceRows(ctx, tenant)
	if err != nil {
		return ReportData{}, fmt.Errorf("compliance rows: %w", err)
	}
	rule, err := st.Rule321Rows(ctx, tenant)
	if err != nil {
		return ReportData{}, fmt.Errorf("rule rows: %w", err)
	}
	return ReportData{
		Tenant: tenant, BrandName: brand, GeneratedAt: now,
		Summary: sum, Compliance: comp, Rule321: rule,
	}, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `gofmt -w internal/report/ && go test ./internal/report/render/`
Expected: PASS (both `TestBuild` and `TestBuildNoAssets`).

- [ ] **Step 6: Commit**

```bash
git add internal/report/render/data.go internal/report/render/data_test.go internal/report/reporttest/reporttest.go
git commit -m "$(printf 'feat(render): ReportData + Build over compliance/rule views\n\nShared per-tenant snapshot model assembled from ReportSummary/ComplianceRows/\nRule321Rows; errors when a tenant has no assets. New reporttest package spins up\na migrated throwaway Postgres for sibling-package tests.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 5: `RenderHTML` (with XSS-escape test)

**Files:**
- Create: `internal/report/render/report.html.tmpl`, `internal/report/render/html.go`
- Test: `internal/report/render/html_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/report/render/html_test.go`:

```go
package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
)

func sampleData() ReportData {
	return ReportData{
		Tenant: "acme", BrandName: "Acme Co",
		GeneratedAt: time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
		Summary:     report.Summary{TotalAssets: 2, CompliantAssets: 1, CopiesFailures: 1, BadgePass: false},
		Compliance: []report.ComplianceRow{
			{AssetID: "x", AssetName: "<script>alert(1)</script>", AssetType: "VMWARE_VIRTUAL_MACHINE",
				Compliant: false, Reasons: "copies"},
		},
		Rule321: []report.Rule321Row{
			{AssetID: "x", AssetName: "vm", ImmutableOk: true, RulePass: false, CopiesCount: 1},
		},
	}
}

func TestRenderHTMLContentAndEscaping(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderHTML(&buf, sampleData()); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Acme Co", "acme", "FAIL", "50%"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// html/template must escape the hostile asset name — no raw <script> in the output.
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("hostile asset name was not escaped (XSS)")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("expected escaped asset name in output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRenderHTML ./internal/report/render/`
Expected: build failure — `undefined: RenderHTML`.

- [ ] **Step 3a: Create the template `internal/report/render/report.html.tmpl`**

```html
<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<title>{{.BrandName}} — {{.Tenant}}</title>
<style>
  body{font-family:system-ui,Arial,sans-serif;margin:2rem;color:#1a1a1a}
  h1{margin:0 0 .25rem} .muted{color:#666;font-size:.9rem}
  .badge{display:inline-block;padding:.4rem .9rem;border-radius:.4rem;font-weight:700;color:#fff}
  .pass{background:#1a7f37}.fail{background:#cf222e}
  table{border-collapse:collapse;width:100%;margin:1rem 0}
  th,td{border:1px solid #ddd;padding:.4rem .6rem;text-align:left;font-size:.9rem}
  th{background:#f3f4f6} .ok{color:#1a7f37}.no{color:#cf222e}
  @media print{body{margin:1rem}}
</style></head><body>
  <h1>{{.BrandName}}</h1>
  <div class="muted">Tenant: {{.Tenant}} · Generated {{.GeneratedAt.Format "2006-01-02 15:04 MST"}}</div>

  <h2>Summary</h2>
  <p>Compliant assets: <strong>{{.Summary.CompliantAssets}}/{{.Summary.TotalAssets}}</strong>
     ({{.CompliantPercent}}%) ·
     3-2-1-1-0 badge: <span class="badge {{if .Summary.BadgePass}}pass{{else}}fail{{end}}">{{.BadgeText}}</span></p>

  <h2>SLA compliance</h2>
  <table><thead><tr><th>Asset</th><th>Type</th><th>Policy</th><th>RPO</th><th>Retention</th><th>Copies</th><th>Compliant</th><th>Reasons</th></tr></thead>
  <tbody>{{range .Compliance}}
    <tr><td>{{.AssetName}}</td><td>{{.AssetType}}</td><td>{{.PolicyName}}</td>
      <td class="{{if .RPOOk}}ok{{else}}no{{end}}">{{if .RPOOk}}✓{{else}}✗{{end}}</td>
      <td class="{{if .RetentionOk}}ok{{else}}no{{end}}">{{if .RetentionOk}}✓{{else}}✗{{end}}</td>
      <td class="{{if .CopiesOk}}ok{{else}}no{{end}}">{{if .CopiesOk}}✓{{else}}✗{{end}}</td>
      <td class="{{if .Compliant}}ok{{else}}no{{end}}">{{if .Compliant}}✓{{else}}✗{{end}}</td>
      <td>{{.Reasons}}</td></tr>{{end}}
  </tbody></table>

  <h2>3-2-1-1-0 backup rule</h2>
  <p class="muted">“2 media” and “1 offsite” are best-effort heuristics over provisional PPDM copy fields.</p>
  <table><thead><tr><th>Asset</th><th>3 copies</th><th>2 media</th><th>1 offsite</th><th>1 immutable</th><th>0 errors</th><th>Rule</th></tr></thead>
  <tbody>{{range .Rule321}}
    <tr><td>{{.AssetName}}</td>
      <td class="{{if .CopiesOk}}ok{{else}}no{{end}}">{{.CopiesCount}}</td>
      <td class="{{if .MediaOk}}ok{{else}}no{{end}}">{{.DistinctMedia}}</td>
      <td class="{{if .OffsiteOk}}ok{{else}}no{{end}}">{{.DistinctLocations}}</td>
      <td class="{{if .ImmutableOk}}ok{{else}}no{{end}}">{{if .ImmutableOk}}✓{{else}}✗{{end}}</td>
      <td class="{{if .ErrorsOk}}ok{{else}}no{{end}}">{{if .ErrorsOk}}✓{{else}}✗{{end}}</td>
      <td class="{{if .RulePass}}ok{{else}}no{{end}}">{{if .RulePass}}PASS{{else}}FAIL{{end}}</td></tr>{{end}}
  </tbody></table>
</body></html>
```

- [ ] **Step 3b: Implement `html.go`**

Create `internal/report/render/html.go`:

```go
package render

import (
	_ "embed"
	"html/template"
	"io"
)

//go:embed report.html.tmpl
var htmlTmplSrc string

var htmlTmpl = template.Must(template.New("report").Parse(htmlTmplSrc))

// RenderHTML writes the branded HTML report. html/template auto-escapes all data, so a hostile
// asset name cannot inject markup.
func RenderHTML(w io.Writer, d ReportData) error {
	return htmlTmpl.Execute(w, d)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/report/render/ && go test -run TestRenderHTML ./internal/report/render/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/render/report.html.tmpl internal/report/render/html.go internal/report/render/html_test.go
git commit -m "$(printf 'feat(render): branded HTML report (html/template, auto-escaped)\n\nEmbedded template + inline CSS renders summary, compliance, and 3-2-1-1-0\ntables. html/template escapes all data; a hostile asset name is rendered as\nentities (asserted). Heuristic caveat noted in the document.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 6: `RenderPDF` (maroto v2)

**Files:**
- Modify: `go.mod`/`go.sum` (add maroto)
- Create: `internal/report/render/pdf.go`
- Test: `internal/report/render/pdf_test.go`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/johnfercher/maroto/v2@latest`
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Write the failing test**

Create `internal/report/render/pdf_test.go`:

```go
package render

import (
	"bytes"
	"testing"
)

func TestRenderPDFProducesPDFBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderPDF(&buf, sampleData()); err != nil {
		t.Fatalf("RenderPDF: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty PDF")
	}
	if got := buf.Bytes()[:5]; string(got) != "%PDF-" {
		t.Fatalf("not a PDF, header = %q", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test -run TestRenderPDF ./internal/report/render/`
Expected: build failure — `undefined: RenderPDF`.

- [ ] **Step 4: Implement `pdf.go`**

Create `internal/report/render/pdf.go`:

```go
package render

import (
	"fmt"
	"io"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// yn renders a boolean as a compact PDF cell value.
func yn(b bool) string {
	if b {
		return "OK"
	}
	return "X"
}

// RenderPDF writes a structured, tabular PDF of the same ReportData (independent layout from the
// HTML — the cost of a browser-free, pure-Go renderer).
func RenderPDF(w io.Writer, d ReportData) error {
	m := maroto.New()

	m.AddRow(10, text.NewCol(12, d.BrandName, props.Text{Style: fontstyle.Bold, Size: 16}))
	m.AddRow(6, text.NewCol(12, fmt.Sprintf("Tenant: %s · Generated %s",
		d.Tenant, d.GeneratedAt.Format("2006-01-02 15:04 MST")), props.Text{Size: 9}))
	m.AddRow(8, text.NewCol(12, fmt.Sprintf("Compliant %d/%d (%d%%) · 3-2-1-1-0 badge: %s",
		d.Summary.CompliantAssets, d.Summary.TotalAssets, d.CompliantPercent(), d.BadgeText()),
		props.Text{Style: fontstyle.Bold, Size: 11}))

	m.AddRow(7, text.NewCol(12, "SLA compliance", props.Text{Style: fontstyle.Bold, Size: 12}))
	m.AddRow(6,
		text.NewCol(5, "Asset", props.Text{Style: fontstyle.Bold}),
		text.NewCol(3, "Policy", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Compliant", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Reasons", props.Text{Style: fontstyle.Bold}),
	)
	for _, r := range d.Compliance {
		m.AddRow(5,
			text.NewCol(5, r.AssetName),
			text.NewCol(3, r.PolicyName),
			text.NewCol(2, yn(r.Compliant)),
			text.NewCol(2, r.Reasons),
		)
	}

	m.AddRow(7, text.NewCol(12, "3-2-1-1-0 backup rule (2-media/1-offsite are provisional heuristics)",
		props.Text{Style: fontstyle.Bold, Size: 12}))
	m.AddRow(6,
		text.NewCol(4, "Asset", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Copies", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Media", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Immutable", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Rule", props.Text{Style: fontstyle.Bold}),
	)
	for _, r := range d.Rule321 {
		m.AddRow(5,
			text.NewCol(4, r.AssetName),
			text.NewCol(2, fmt.Sprintf("%d", r.CopiesCount)),
			text.NewCol(2, fmt.Sprintf("%d", r.DistinctMedia)),
			text.NewCol(2, yn(r.ImmutableOk)),
			text.NewCol(2, yn(r.RulePass)),
		)
	}

	doc, err := m.Generate()
	if err != nil {
		return fmt.Errorf("generate pdf: %w", err)
	}
	_, err = w.Write(doc.GetBytes())
	return err
}
```

> If `go build` reports that `doc.GetBytes()` does not exist for the installed maroto version, check the `core.Document` interface in the vendored maroto (`go doc github.com/johnfercher/maroto/v2/pkg/core.Document`) — v2 exposes `GetBytes() []byte` and `GetBase64() string`. Use whichever the installed version provides; `GetBytes` is expected.

- [ ] **Step 5: Run test to verify it passes**

Run: `gofmt -w internal/report/render/ && go test -run TestRenderPDF ./internal/report/render/`
Expected: PASS (output begins `%PDF-`).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/report/render/pdf.go internal/report/render/pdf_test.go
git commit -m "$(printf 'feat(render): pure-Go PDF report (maroto v2)\n\nBrowser-free tabular PDF of the same ReportData (summary, compliance,\n3-2-1-1-0). Keeps the static, multi-arch build. Verified: output begins %%PDF-.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 7: `report render` CLI subcommand

**Files:**
- Modify: `cmd/report/main.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/report/render_test.go`:

```go
package main

import "testing"

// formatExt maps a --format flag to a file extension; "" defaults to html.
func TestFormatExt(t *testing.T) {
	cases := map[string]string{"": "html", "html": "html", "pdf": "pdf"}
	for in, want := range cases {
		if got, err := formatExt(in); err != nil || got != want {
			t.Errorf("formatExt(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := formatExt("docx"); err == nil {
		t.Error("expected error for unknown format")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestFormatExt ./cmd/report/`
Expected: build failure — `undefined: formatExt`.

- [ ] **Step 3: Add the subcommand + helper to `main.go`**

Add imports: `"io"`, `"os"`, `"time"`, `"github.com/fjacquet/ppdm_exporter/internal/report/render"`. Add the helper and command:

```go
// formatExt validates a --format value and returns its file extension. Empty defaults to html.
func formatExt(format string) (string, error) {
	switch format {
	case "", "html":
		return "html", nil
	case "pdf":
		return "pdf", nil
	default:
		return "", fmt.Errorf("unsupported format %q (want html or pdf)", format)
	}
}

func renderCommand() *cobra.Command {
	var cfgPath, tenant, format, out string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render a tenant's backup-assurance report (html or pdf) to a file",
		RunE: func(_ *cobra.Command, _ []string) error {
			ext, err := formatExt(format)
			if err != nil {
				return err
			}
			if tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			cfg, err := config.LoadReport(cfgPath)
			if err != nil {
				return err
			}
			ctx := context.Background()
			store, err := report.New(ctx, cfg.Database.DSN)
			if err != nil {
				return err
			}
			defer store.Close()
			data, err := render.Build(ctx, store, tenant, cfg.Report.BrandName, time.Now())
			if err != nil {
				return err
			}
			var w io.Writer = os.Stdout
			if out != "" {
				f, err := os.Create(out)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				w = f
			}
			if ext == "pdf" {
				return render.RenderPDF(w, data)
			}
			return render.RenderHTML(w, data)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "config.report.yaml", "path to config file")
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant to report on (required)")
	cmd.Flags().StringVar(&format, "format", "html", "output format: html or pdf")
	cmd.Flags().StringVar(&out, "out", "", "output file (default: stdout)")
	return cmd
}
```

Wire it into `main()` by adding `root.AddCommand(renderCommand())` after the root flags are set.

- [ ] **Step 4: Run test + build to verify**

Run: `gofmt -w cmd/report/ && go test -run TestFormatExt ./cmd/report/ && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/report/main.go cmd/report/render_test.go
git commit -m "$(printf 'feat(report): report render CLI subcommand\n\nrender --tenant X --format html|pdf --out FILE builds ReportData and writes the\nchosen renderer to a file (stdout by default). Reuses config + report.Store.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 8: HTTP endpoint (`GET /report`, `/healthz`)

**Files:**
- Create: `internal/report/render/http.go`, `internal/report/render/http_test.go`
- Modify: `cmd/report/main.go` (serve when `report.listen` set)

- [ ] **Step 1: Write the failing test**

Create `internal/report/render/http_test.go`:

```go
package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesReport(t *testing.T) {
	st := storeFor(t) // from data_test.go: tenant acme has assets
	h := NewHandler(st, "Acme Co", "") // no auth
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/report?tenant=acme&format=html")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
}

func TestHandlerValidation(t *testing.T) {
	st := storeFor(t)
	h := NewHandler(st, "Acme Co", "tok")
	srv := httptest.NewServer(h)
	defer srv.Close()

	do := func(path string, bearer string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if c := do("/report?tenant=acme", ""); c != 401 {
		t.Errorf("no token -> %d, want 401", c)
	}
	if c := do("/report?tenant=acme", "wrong"); c != 401 {
		t.Errorf("bad token -> %d, want 401", c)
	}
	if c := do("/report", "tok"); c != 400 {
		t.Errorf("missing tenant -> %d, want 400", c)
	}
	if c := do("/report?tenant=ghost", "tok"); c != 404 {
		t.Errorf("unknown tenant -> %d, want 404", c)
	}
	if c := do("/report?tenant=acme", "tok"); c != 200 {
		t.Errorf("good token -> %d, want 200", c)
	}
}

func TestHandlerRejectsNonGET(t *testing.T) {
	st := reporttest.NewStore(t)
	srv := httptest.NewServer(NewHandler(st, "B", ""))
	defer srv.Close()
	resp, _ := http.Post(srv.URL+"/report?tenant=acme", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST -> %d, want 405", resp.StatusCode)
	}
}
```

Add the import `"github.com/fjacquet/ppdm_exporter/internal/report/reporttest"` to `http_test.go` (used by `TestHandlerRejectsNonGET`; `storeFor` already pulls it in via `data_test.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestHandler ./internal/report/render/`
Expected: build failure — `undefined: NewHandler`.

- [ ] **Step 3: Implement `http.go`**

Create `internal/report/render/http.go`:

```go
package render

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	log "github.com/sirupsen/logrus"
)

// NewHandler returns the read-only report HTTP surface: GET /report?tenant=&format= and
// GET /healthz. When authToken is non-empty, /report requires a matching Bearer token.
func NewHandler(st *report.Store, brand, authToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if authToken != "" && !bearerOK(r, authToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			http.Error(w, "tenant is required", http.StatusBadRequest)
			return
		}
		format := r.URL.Query().Get("format")
		ext, err := formatExtHTTP(format)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, err := Build(r.Context(), st, tenant, brand, time.Now())
		if errors.Is(err, ErrNoData) {
			http.Error(w, "no report for tenant", http.StatusNotFound)
			return
		} else if err != nil {
			log.WithError(err).Warn("build report failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Render to a buffer first so a render error never yields a half-written 200 body.
		var buf bytes.Buffer
		if ext == "pdf" {
			err = RenderPDF(&buf, data)
		} else {
			err = RenderHTML(&buf, data)
		}
		if err != nil {
			log.WithError(err).Warn("render report failed")
			http.Error(w, "render failed", http.StatusInternalServerError)
			return
		}
		if ext == "pdf" {
			w.Header().Set("Content-Type", "application/pdf")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		_, _ = w.Write(buf.Bytes())
	})
	return mux
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "no-store")
}

func bearerOK(r *http.Request, token string) bool {
	h := r.Header.Get("Authorization")
	got := strings.TrimPrefix(h, "Bearer ")
	if got == h { // prefix not present
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func formatExtHTTP(format string) (string, error) {
	switch format {
	case "", "html":
		return "html", nil
	case "pdf":
		return "pdf", nil
	default:
		return "", errUnsupportedFormat
	}
}
```

> `errUnsupportedFormat` is already declared in `data.go` (Task 4), so `http.go` reuses it.

> Note: the CSP allows `style-src 'unsafe-inline'` because the HTML report uses an inline `<style>` block; everything else is `'none'`. The PDF response is binary and unaffected.

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/report/render/ && go test -run TestHandler ./internal/report/render/`
Expected: PASS (200/401/400/404/405 all as asserted; `nosniff` present).

- [ ] **Step 5: Serve the handler from the capture process**

In `cmd/report/main.go` `run(...)`, after the store is migrated and before starting the capture loop, start the HTTP server when configured. Add import `"net/http"`. Insert:

```go
	if cfg.Report.Listen != "" {
		h := render.NewHandler(store, cfg.Report.BrandName, cfg.Report.AuthToken)
		srv := &http.Server{Addr: cfg.Report.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.WithField("addr", cfg.Report.Listen).Info("serving report endpoint")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.WithError(err).Error("report endpoint failed")
			}
		}()
		defer func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutCtx)
		}()
	}
```

- [ ] **Step 6: Run build + full render/cmd tests**

Run: `gofmt -w ./internal ./cmd && go build ./... && go test ./internal/report/render/ ./cmd/report/`
Expected: clean build, all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/report/render/http.go internal/report/render/http_test.go cmd/report/main.go
git commit -m "$(printf 'feat(report): read-only GET /report HTTP endpoint\n\nServed by the capture process when report.listen is set. GET-only, tenant\nrequired (400) / unknown (404), optional constant-time bearer auth (401),\nsecurity headers, render-to-buffer before write. /healthz for liveness.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 9: Demo, docs, changelog

**Files:**
- Modify: `config.report.demo.yaml` (add a `report:` block), `docs/report.md`, `CHANGELOG.md`

- [ ] **Step 1: Add a `report` block to the demo config**

Append to `config.report.demo.yaml`:

```yaml
report:
  listen: "0.0.0.0:9103"
  brandName: "Acme Backup Assurance"
```

- [ ] **Step 2: Document Phase 3 in `docs/report.md`**

Append a section:

```markdown
## Assurance report (Phase 3)

Render a tenant's current-snapshot report — SLA compliance verdicts plus a 3-2-1-1-0
backup-rule badge — as branded HTML or pure-Go PDF:

```bash
./bin/report render --tenant acme-corp --format pdf --out acme.pdf
./bin/report render --tenant acme-corp --format html > acme.html
```

When `report.listen` is set, the capture process also serves the report read-only:

```bash
curl 'http://127.0.0.1:9103/report?tenant=acme-corp&format=html'
# with auth: -H "Authorization: Bearer $REPORT_TOKEN"
```

> The 3-2-1-1-0 “2 media” and “1 offsite” checks are best-effort heuristics over provisional
> PPDM copy fields (`storage_system_id`, `location`); the report labels them as such.
```

- [ ] **Step 3: Add a CHANGELOG entry**

Under the unreleased section of `CHANGELOG.md`, add:

```markdown
- `cmd/report` Phase 3: branded backup-assurance report (HTML via html/template + pure-Go PDF
  via maroto v2) over the compliance view, with a computed 3-2-1-1-0 badge (`rule_321110`
  view). Generated via `report render` CLI and an opt-in read-only `GET /report` endpoint.
```

- [ ] **Step 4: Commit**

```bash
git add config.report.demo.yaml docs/report.md CHANGELOG.md
git commit -m "$(printf 'docs(report): Phase 3 report usage + demo report block + changelog\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 10: Final gate + security pass

- [ ] **Step 1: Run the full CI gate**

Run: `make ci`
Expected: gofmt/vet clean, golangci-lint `0 issues`, `go test -race ./...` all pass, govulncheck `No vulnerabilities found`, build succeeds.

- [ ] **Step 2: Semgrep-scan the new code**

Scan (via the semgrep MCP tool, or `semgrep --config auto`) these files; expect 0 findings:
`internal/report/render/*.go`, `internal/report/store.go`, `internal/config/report.go`, `cmd/report/main.go`. No inline suppressions — restructure if flagged.

- [ ] **Step 3: Manual end-to-end (optional, needs Docker)**

Run: `make demo` then in another shell:
`docker compose exec report /report render --tenant acme-corp --format html --out /tmp/r.html && echo OK`
and `curl -s 'http://127.0.0.1:9103/report?tenant=acme-corp' | head -c 200`.
Expected: HTML containing the badge verdict; the demo copies (one DD system, one location, one
immutable copy) yield a realistic partial badge (immutable ✓, copies/media/offsite ✗).

- [ ] **Step 4: Security review of the HTTP surface**

Because Phase 3 adds a network surface, run `/security-review` on the branch (focus: `render/http.go`
auth/headers, template escaping, SQL parameterization). Address any high-confidence findings.

- [ ] **Step 5: Final commit (if any fixes) and push**

```bash
git add -A && git commit -m "$(printf 'chore(report): Phase 3 gate fixes\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')" || true
git push -u origin feat/backup-report-phase3
```

---

## Self-review notes (spec coverage)

- Spec §1 `rule_321110` view → Task 1. §2 store read methods → Tasks 1–2. §3 ReportData/Build → Task 4; HTML → Task 5; PDF → Task 6. §4 CLI → Task 7; HTTP (auth/headers/validation) → Task 8. §5 config → Task 3. §6 testing → tests embedded in every task + Task 10 gate. §7 out-of-scope respected (no scheduling/trends/ISO mapping).
- Type names consistent across tasks: `report.ComplianceRow`, `report.Rule321Row`, `report.Summary`, `render.ReportData`, `render.Build/RenderHTML/RenderPDF/NewHandler`, `formatExt` (CLI) / `formatExtHTTP` (HTTP, distinct to avoid duplicate-symbol).
- Provisional-field caveat (2-media/1-offsite) surfaced in the view comment, the HTML, the PDF, and the docs.
