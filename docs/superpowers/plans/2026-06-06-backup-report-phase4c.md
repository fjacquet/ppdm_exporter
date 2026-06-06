# Backup Reporter Phase 4c — Per-Tenant Retention — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prune `backup_jobs`/`copies` and set each tenant's first-capture backfill window from a per-tenant retention config (default + overrides) instead of one global `retentionDays`.

**Architecture:** A `config.Retention{DefaultDays, Overrides}` block with `DaysFor(tenant)` drives both `Store.Prune` (per-tenant cutoffs + a default sweep for the rest) and `Capturer.bootstrap(tenant)`. `defaultDays` falls back to the existing `capture.retentionDays` for back-compat.

**Tech Stack:** Go 1.26 stdlib + pgx v5 (`text[]` array param). No new dependencies.

Spec: `docs/superpowers/specs/2026-06-06-backup-report-phase4c-design.md`.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/config/report.go` (modify) | `Retention`/`RetentionOverride` types, `DaysFor`, field, fallback + validation |
| `internal/config/report_test.go` (modify) | retention parse/fallback/DaysFor/validation tests |
| `internal/report/store.go` (modify) | `Prune(defaultDays, overrides)` per-tenant |
| `internal/report/store_retention_test.go` (create) | per-tenant prune testcontainers test |
| `internal/report/watermark_test.go` (modify) | update the existing `Prune(ctx, 400)` call |
| `internal/report/capture.go` (modify) | `Capturer.retention`, `NewCapturer(config.Retention)`, `bootstrap(tenant)`, `RunOnce` prune |
| `internal/report/capture_test.go`, `internal/report/sla_test.go` (modify) | update `NewCapturer(...)` call sites |
| `cmd/report/main.go` (modify) | pass `cfg.Retention` |
| `config.report.demo.yaml`, `docs/report.md`, `CHANGELOG.md` (modify) | demo + docs |

**Conventions:** parameterized SQL only; no inline lint/semgrep suppressions; `gofmt -w` before each commit; commit trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. Real-Postgres tests via `newTestStore(t)` (no `-short`). `internal/report` already imports `internal/config`.

---

## Task 1: Config — `retention` block

**Files:**
- Modify: `internal/config/report.go`
- Test: `internal/config/report_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/report_test.go`:

```go
func TestLoadReportRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
retention:
  defaultDays: 400
  overrides:
    - {tenant: acme-corp, days: 730}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Retention.DefaultDays != 400 {
		t.Errorf("defaultDays = %d", cfg.Retention.DefaultDays)
	}
	if cfg.Retention.DaysFor("acme-corp") != 730 {
		t.Errorf("override = %d, want 730", cfg.Retention.DaysFor("acme-corp"))
	}
	if cfg.Retention.DaysFor("other") != 400 {
		t.Errorf("default = %d, want 400", cfg.Retention.DaysFor("other"))
	}
}

func TestLoadReportRetentionBackCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	// No retention block; capture.retentionDays seeds retention.defaultDays.
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
capture: {retentionDays: 200}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Retention.DefaultDays != 200 {
		t.Errorf("defaultDays = %d, want 200 (from capture.retentionDays)", cfg.Retention.DefaultDays)
	}
}

func TestLoadReportRetentionValidation(t *testing.T) {
	base := "database: {dsn: x}\nservers:\n  - {name: p, host: h, username: u, password: p}\n"
	cases := map[string]string{
		"override days 0": base + "retention:\n  overrides:\n    - {tenant: a, days: 0}\n",
		"empty tenant":    base + "retention:\n  overrides:\n    - {tenant: \"\", days: 30}\n",
		"negative default": base + "retention:\n  defaultDays: -5\n",
	}
	for name, y := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "r.yaml")
		_ = os.WriteFile(path, []byte(y), 0o600)
		if _, err := LoadReport(path); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestLoadReportRetention ./internal/config/`
Expected: build failure — `cfg.Retention undefined`.

- [ ] **Step 3: Add types, DaysFor, field, fallback + validation**

Add the types + method (near the other report config types):

```go
// RetentionOverride sets a per-tenant retention window (days) overriding the default.
type RetentionOverride struct {
	Tenant string `yaml:"tenant"`
	Days   int    `yaml:"days"`
}

// Retention configures how long captured history is kept per tenant. DefaultDays applies to any
// tenant without an override; it falls back to capture.retentionDays when unset.
type Retention struct {
	DefaultDays int                 `yaml:"defaultDays"`
	Overrides   []RetentionOverride `yaml:"overrides"`
}

// DaysFor returns the retention window for a tenant: its override if present, else DefaultDays.
func (r Retention) DaysFor(tenant string) int {
	for _, o := range r.Overrides {
		if o.Tenant == tenant {
			return o.Days
		}
	}
	return r.DefaultDays
}
```

Add the field to `ReportConfig` (alongside `Compliance`/`Report`):

```go
	Retention  Retention      `yaml:"retention"`
}
```

In `LoadReport`, **after** the `cfg.Capture.RetentionDays` default block (so the fallback sees the
defaulted 400), add:

```go
	if cfg.Retention.DefaultDays == 0 {
		cfg.Retention.DefaultDays = cfg.Capture.RetentionDays
	}
	if cfg.Retention.DefaultDays <= 0 {
		return nil, fmt.Errorf("retention.defaultDays must be > 0")
	}
	for i, o := range cfg.Retention.Overrides {
		if o.Tenant == "" {
			return nil, fmt.Errorf("retention override %d: tenant required", i)
		}
		if o.Days <= 0 {
			return nil, fmt.Errorf("retention override %d (%s): days must be > 0", i, o.Tenant)
		}
	}
```

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/config/ && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/report.go internal/config/report_test.go
git commit -m "$(printf 'feat(config): per-tenant retention block\n\nRetention{defaultDays, overrides[]} + DaysFor(tenant); defaultDays falls back to\ncapture.retentionDays (back-compat). Validates defaultDays>0 and each override\n(non-empty tenant, days>0).\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 2: report package — per-tenant Prune + Capturer retention

**Files:**
- Modify: `internal/report/store.go`, `internal/report/capture.go`, `internal/report/watermark_test.go`, `internal/report/capture_test.go`, `internal/report/sla_test.go`
- Test: `internal/report/store_retention_test.go` (create)

> Note: `Store.Prune`'s signature and `Capturer`'s retention field change together (RunOnce calls Prune
> with the Capturer's retention), so they are one task to keep the `report` package compiling. After
> this task `go build ./...` fails at `cmd/report/main.go` (still passing an `int`) — that is the
> expected interim break, fixed in Task 3. Verify this task with `go test ./internal/report/`.

- [ ] **Step 1: Write the failing test**

Create `internal/report/store_retention_test.go`:

```go
package report

import (
	"context"
	"testing"
)

func TestPrunePerTenant(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	// Two tenants, each with an old (~500d) and a recent (~1d) job + copy.
	exec(`INSERT INTO backup_jobs (id,tenant,server,created_at,captured_at) VALUES
		('ja_old','acme','s1', now()-interval '500 days', now()),
		('ja_new','acme','s1', now()-interval '1 day', now()),
		('jg_old','globex','s1', now()-interval '500 days', now()),
		('jg_new','globex','s1', now()-interval '1 day', now())`)
	exec(`INSERT INTO copies (id,tenant,server,create_time,captured_at) VALUES
		('ca_old','acme','s1', now()-interval '500 days', now()),
		('ca_new','acme','s1', now()-interval '1 day', now()),
		('cg_old','globex','s1', now()-interval '500 days', now()),
		('cg_new','globex','s1', now()-interval '1 day', now())`)

	// acme keeps 730 days; globex falls to the 400-day default.
	if err := st.Prune(ctx, 400, map[string]int{"acme": 730}); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	has := func(table, id string) bool {
		var n int
		_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE id=$1", id).Scan(&n)
		return n == 1
	}
	// acme's 500d rows survive (within 730); recent rows survive everywhere.
	for _, c := range []struct {
		table, id string
		want      bool
	}{
		{"backup_jobs", "ja_old", true},   // acme override 730 > 500
		{"backup_jobs", "ja_new", true},
		{"backup_jobs", "jg_old", false},  // globex default 400 < 500 -> pruned
		{"backup_jobs", "jg_new", true},
		{"copies", "ca_old", true},
		{"copies", "ca_new", true},
		{"copies", "cg_old", false},
		{"copies", "cg_new", true},
	} {
		if got := has(c.table, c.id); got != c.want {
			t.Errorf("%s/%s present=%v, want %v", c.table, c.id, got, c.want)
		}
	}
}

func TestPruneEmptyOverridesIsGlobal(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.pool.Exec(ctx, `INSERT INTO backup_jobs (id,tenant,server,created_at,captured_at)
		VALUES ('old','t','s1', now()-interval '500 days', now()), ('new','t','s1', now()-interval '1 day', now())`); err != nil {
		t.Fatal(err)
	}
	if err := st.Prune(ctx, 400, nil); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	var old, recent int
	_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM backup_jobs WHERE id='old'").Scan(&old)
	_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM backup_jobs WHERE id='new'").Scan(&recent)
	if old != 0 || recent != 1 {
		t.Errorf("global prune: old=%d (want 0) new=%d (want 1)", old, recent)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestPrune ./internal/report/`
Expected: build failure — `Prune` signature mismatch (`too many arguments`).

- [ ] **Step 3a: Rewrite `Store.Prune` (`store.go`)**

Replace the existing `Prune` with:

```go
// Prune deletes append-only event rows (backup_jobs, copies) older than each tenant's retention
// window: per-tenant for override tenants, then a default sweep for every other tenant. assets and
// protection_policies hold current upsert-latest state and are intentionally not pruned.
func (s *Store) Prune(ctx context.Context, defaultDays int, overrides map[string]int) error {
	now := time.Now()
	tenants := make([]string, 0, len(overrides))
	for tenant, days := range overrides {
		tenants = append(tenants, tenant)
		cutoff := now.AddDate(0, 0, -days)
		if _, err := s.pool.Exec(ctx, `DELETE FROM backup_jobs WHERE tenant=$1 AND created_at < $2`, tenant, cutoff); err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM copies WHERE tenant=$1 AND create_time < $2`, tenant, cutoff); err != nil {
			return err
		}
	}
	// Default sweep for all tenants not covered by an override. An empty `tenants` array makes
	// `tenant <> ALL('{}')` true for every row, degenerating to a global prune at defaultDays.
	defCutoff := now.AddDate(0, 0, -defaultDays)
	if _, err := s.pool.Exec(ctx, `DELETE FROM backup_jobs WHERE tenant <> ALL($1::text[]) AND created_at < $2`, tenants, defCutoff); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM copies WHERE tenant <> ALL($1::text[]) AND create_time < $2`, tenants, defCutoff)
	return err
}
```

- [ ] **Step 3b: Update the existing Prune caller in `watermark_test.go`**

Change `internal/report/watermark_test.go` line ~28 from `st.Prune(ctx, 400)` to:

```go
	if err := st.Prune(ctx, 400, nil); err != nil { // ~13 months, global
```

- [ ] **Step 3c: Update the `Capturer` (`capture.go`)**

Change the struct field and constructor:

```go
// Capturer pulls authoritative PPDM records for one server and persists them.
type Capturer struct {
	store      *Store
	version    string
	retention  config.Retention
	compliance config.Compliance
}

// NewCapturer wires a capturer to a store. retention drives per-tenant prune + backfill; compliance
// drives post-capture SLA target resolution.
func NewCapturer(store *Store, version string, retention config.Retention, compliance config.Compliance) *Capturer {
	return &Capturer{store: store, version: version, retention: retention, compliance: compliance}
}
```

Change `bootstrap` to take the tenant:

```go
// bootstrap returns the watermark, or now minus the tenant's retention window when there is no prior
// data, so the first capture backfills that tenant's history without fetching the entire server.
func (c *Capturer) bootstrap(tenant string, wm time.Time) time.Time {
	if wm.IsZero() {
		return time.Now().AddDate(0, 0, -c.retention.DaysFor(tenant))
	}
	return wm
}
```

Update the two `bootstrap` calls in `capture(...)` (both have `tenant` in scope): change
`c.bootstrap(jobWM)` → `c.bootstrap(tenant, jobWM)` and `c.bootstrap(copyWM)` → `c.bootstrap(tenant, copyWM)`.

Change the prune call in `RunOnce` (replace `if err := c.store.Prune(ctx, c.retentionDays); err != nil {`):

```go
	overrides := make(map[string]int, len(c.retention.Overrides))
	for _, o := range c.retention.Overrides {
		overrides[o.Tenant] = o.Days
	}
	if err := c.store.Prune(ctx, c.retention.DefaultDays, overrides); err != nil {
		log.WithError(err).Warn("prune failed")
	}
```

- [ ] **Step 3d: Update the `NewCapturer` test call sites**

In `internal/report/capture_test.go` (2 sites) and `internal/report/sla_test.go` (2 sites), change
`NewCapturer(st, "v-test", 400, <compliance>)` → `NewCapturer(st, "v-test", config.Retention{DefaultDays: 400}, <compliance>)`
(keep each existing compliance arg: `config.Compliance{}` or `goldVMConfig()`). Both test files already
import `internal/config`.

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/report/ && go test ./internal/report/`
Expected: PASS (incl. `TestPrunePerTenant`, `TestPruneEmptyOverridesIsGlobal`, and the existing capture/sla tests). `go build ./...` will still fail at `cmd/report/main.go` — that is fixed in Task 3.

- [ ] **Step 5: Commit**

```bash
git add internal/report/store.go internal/report/capture.go internal/report/watermark_test.go internal/report/store_retention_test.go internal/report/capture_test.go internal/report/sla_test.go
git commit -m "$(printf 'feat(report): per-tenant prune + backfill from Retention\n\nStore.Prune(defaultDays, overrides) deletes per-tenant cutoffs then a default\nsweep (tenant <> ALL) for the rest; empty overrides = global prune. Capturer holds\nconfig.Retention; bootstrap(tenant) backfills the tenant window; RunOnce builds the\noverride map. NewCapturer takes config.Retention.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 3: Wire retention in `cmd/report`

**Files:**
- Modify: `cmd/report/main.go`

- [ ] **Step 1: Update the wiring**

Change `cmd/report/main.go` line ~115 from
`capt := report.NewCapturer(store, version, cfg.Capture.RetentionDays, cfg.Compliance)` to:

```go
	capt := report.NewCapturer(store, version, cfg.Retention, cfg.Compliance)
```

- [ ] **Step 2: Build + vet (fixes the Task-2 interim break)**

Run: `gofmt -w cmd/report/ && go build ./... && go vet ./...`
Expected: clean (the branch builds again).

- [ ] **Step 3: Commit**

```bash
git add cmd/report/main.go
git commit -m "$(printf 'feat(report): pass cfg.Retention to the capturer\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 4: Demo + docs

**Files:**
- Modify: `config.report.demo.yaml`, `docs/report.md`, `CHANGELOG.md`

- [ ] **Step 1: Add a retention block to the demo config**

Append to `config.report.demo.yaml` (read it first; add at top level, not inside another block):

```yaml
retention:
  defaultDays: 400
  overrides:
    - {tenant: acme-corp, days: 730}   # keep acme-corp's history for 2 years
```

- [ ] **Step 2: Document retention in `docs/report.md`**

Append:

````markdown
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
````

- [ ] **Step 3: CHANGELOG entry**

Under `## [Unreleased]` → `### Added` in `CHANGELOG.md` (read to find the spot; below the Phase 4b bullet):

```markdown
- `cmd/report` Phase 4c: per-tenant retention — a `retention` block (defaultDays + per-tenant
  overrides) prunes `backup_jobs`/`copies` and sets each tenant's first-capture backfill window;
  `defaultDays` falls back to `capture.retentionDays`. Completes Phase 4.
```

- [ ] **Step 4: Validate + commit**

Run: `ruby -ryaml -e 'YAML.safe_load(File.read("config.report.demo.yaml"))' && docker compose config -q` (expect no error).

```bash
git add config.report.demo.yaml docs/report.md CHANGELOG.md
git commit -m "$(printf 'docs(report): Phase 4c retention docs + demo override\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 5: Gate + push

- [ ] **Step 1: Full CI gate**

Run: `make ci`
Expected: gofmt/vet clean, golangci-lint `0 issues`, `go test -race ./...` pass, govulncheck `No vulnerabilities found`, build OK.

- [ ] **Step 2: Semgrep**

Scan the changed Go files (`internal/report/store.go`, `internal/report/capture.go`,
`internal/config/report.go`, `cmd/report/main.go`) via the semgrep MCP tool or `semgrep --config auto`.
Expect 0 findings; no inline suppressions.

- [ ] **Step 3: Push**

```bash
git push -u origin feat/backup-report-phase4c
```

---

## Self-review notes (spec coverage)

- Spec §1 config (Retention + DaysFor + fallback + validation) → Task 1. §2 Capturer (retention field,
  NewCapturer(config.Retention), bootstrap(tenant), RunOnce overrides) → Task 2. §3 Store.Prune
  (per-tenant + default sweep, empty-overrides degenerate) → Task 2. §4 wiring → Task 3. Testing → tests
  in Tasks 1-2 + Task 5 gate. Demo/docs → Task 4.
- Type/name consistency: `config.Retention{DefaultDays, Overrides}`, `config.RetentionOverride{Tenant,
  Days}`, `Retention.DaysFor(tenant) int`; `Store.Prune(ctx, defaultDays int, overrides map[string]int)`;
  `NewCapturer(store, version string, retention config.Retention, compliance config.Compliance)`;
  `bootstrap(tenant string, wm time.Time)`.
- Coupled-change rationale documented (Task 2 changes Prune + Capturer together; main.go fixed in Task 3
  — the interim `go build ./...` break is expected, mirroring Phase 4b's Task 3→4).
- All call sites updated: `Prune` (watermark_test.go + RunOnce), `NewCapturer` (capture_test x2,
  sla_test x2, main.go).
