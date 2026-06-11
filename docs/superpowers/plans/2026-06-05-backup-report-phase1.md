# Backup Report — Phase 1 (Durable history store) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A second binary `cmd/report` that periodically pulls authoritative backup records (activities, copies, assets, protection-policies) from each configured PPDM server and upserts them into PostgreSQL, retained long-term and tagged per tenant — the durable history Grafana and later phases report on.

**Architecture:** A capture loop (mirroring the exporter's loop) reuses the existing `internal/ppdmclient` (bearer auth + generic `GetAll[T]`) — **no client change needed**, since `GetAll` is generic over the caller's struct. Jobs/copies are captured incrementally by a `createdAt`/`createTime` watermark and inserted append-only; assets/policies are pulled in full and upserted-latest. A `capture_runs` table records provenance. The exporter binary and its image are untouched.

**Tech Stack:** Go 1.26.4, `github.com/jackc/pgx/v5` (+ `pgxpool`), embedded SQL schema (`CREATE TABLE IF NOT EXISTS`, run on startup), `github.com/testcontainers/testcontainers-go` (+ `modules/postgres`) for DB tests, reusing `internal/ppdmclient` + `internal/config` patterns, `spf13/cobra`, `sirupsen/logrus`, `golang.org/x/sync/errgroup`.

**Spec:** `docs/superpowers/specs/2026-06-05-backup-report-phase1-design.md`

> ⚠️ **Provisional PPDM shapes.** Copy/policy field names are modeled from the 19.22.0 API
> reference; tagged `// provisional` and added to the exporter's ADR-0009 validation list.

---

## File Structure

| File | Responsibility |
|---|---|
| `cmd/report/main.go` | cobra CLI (`--config --debug --once`), wiring, capture loop start |
| `internal/config/report.go` | `ReportConfig` + `LoadReport` (reuses `interpolate`/`envRef`) |
| `internal/report/models.go` | PPDM capture DTOs (`Job`, `Copy`, `Asset`, `Policy`) + time parse helpers |
| `internal/report/store.go` | `Store` (pgx pool), `Migrate`, upserts, watermark, prune, `RecordRun` |
| `internal/report/migrations.sql` | Embedded `CREATE TABLE IF NOT EXISTS` schema |
| `internal/report/capture.go` | `Capturer`: `CaptureServer` (pull→upsert→provenance), `Run` loop |
| `internal/report/*_test.go` | Unit (mock client) + DB tests (testcontainers) |
| `config.report.yaml`, `config.report.demo.yaml` | Sample configs |
| `cmd/mockppdm/fixtures/{copies,protection-policies}.json` | New demo fixtures |
| `docker-compose.yml`, `grafana/provisioning/datasources/postgres.yml`, `grafana/dashboards/ppdm-backup-history.json`, `Makefile` | Demo stack: Postgres + report + Grafana history view |

> **Standing convention:** add a `CHANGELOG.md` `[Unreleased]` line for each user-visible change.

---

## Phase 0 — Scaffold

### Task 1: `cmd/report` skeleton + deps + build

**Files:** Create `cmd/report/main.go`. Modify `go.mod`.

- [ ] **Step 1: Minimal `cmd/report/main.go`**

```go
// Command report captures PowerProtect Data Manager backup history into PostgreSQL
// for assurance reporting (durable history; Grafana + branded reports read it).
package main

import "fmt"

var version = "dev"

func main() { fmt.Printf("report %s\n", version) }
```

- [ ] **Step 2: Build**

Run: `go build ./cmd/report/ && go run ./cmd/report/`
Expected: prints `report dev`

- [ ] **Step 3: Add deps**

Run: `go get github.com/jackc/pgx/v5@latest github.com/testcontainers/testcontainers-go@latest github.com/testcontainers/testcontainers-go/modules/postgres@latest`

- [ ] **Step 4: Add a `report-cli` Makefile target**

Append to `Makefile`:
```makefile
report-cli:
	go build -ldflags "$(LDFLAGS)" -o bin/report ./cmd/report
```

- [ ] **Step 5: Commit**

```bash
git add cmd/report/main.go go.mod go.sum Makefile
git commit -m "chore(report): scaffold cmd/report binary"
```

---

## Phase 1 — Config

### Task 2: ReportConfig + LoadReport

**Files:** Create `internal/config/report.go`, `internal/config/report_test.go`.

Reuses the existing `interpolate`/`envRef` in `internal/config/config.go` (same package).

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReportInterpolatesAndDefaults(t *testing.T) {
	t.Setenv("PG_PASSWORD", "pgsecret")
	t.Setenv("PPDM1_PASSWORD", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")
	yaml := `
database: {dsn: "postgres://u:${PG_PASSWORD}@localhost:5432/backup_report?sslmode=disable"}
capture: {interval: "1h", timeout: "5m", retentionDays: 400}
servers:
  - {name: ppdm01, tenant: acme, host: h, username: u, password: "${PPDM1_PASSWORD}", insecureSkipVerify: true}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Database.DSN != "postgres://u:pgsecret@localhost:5432/backup_report?sslmode=disable" {
		t.Fatalf("dsn = %q", cfg.Database.DSN)
	}
	if cfg.Servers[0].Tenant != "acme" || cfg.Servers[0].Password != "s3cret" {
		t.Fatalf("server = %+v", cfg.Servers[0])
	}
	if cfg.Capture.RetentionDays != 400 || cfg.Capture.Interval.String() != "1h0m0s" {
		t.Fatalf("capture = %+v", cfg.Capture)
	}
}

func TestLoadReportRejectsNoServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(path, []byte("database: {dsn: x}\nservers: []\n"), 0o600)
	if _, err := LoadReport(path); err == nil {
		t.Fatal("expected error for no servers")
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: LoadReport` (`go test ./internal/config/ -run TestLoadReport -v`)

- [ ] **Step 3: Implementation** — `internal/config/report.go`

```go
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// ReportServer is one PPDM server captured for backup history, tagged with a tenant.
type ReportServer struct {
	Name               string `yaml:"name"`
	Tenant             string `yaml:"tenant"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"` // defaults to 8443
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"passwordFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// BaseURL returns the https://host:port root for the PPDM REST API.
func (s ReportServer) BaseURL() string {
	port := s.Port
	if port == 0 {
		port = 8443
	}
	return fmt.Sprintf("https://%s:%d", s.Host, port)
}

// ReportConfig is the cmd/report configuration.
type ReportConfig struct {
	Database struct {
		DSN string `yaml:"dsn"`
	} `yaml:"database"`
	Capture struct {
		Interval      time.Duration `yaml:"interval"`
		Timeout       time.Duration `yaml:"timeout"`
		RetentionDays int           `yaml:"retentionDays"`
	} `yaml:"capture"`
	Servers []ReportServer `yaml:"servers"`
}

// LoadReport reads, interpolates ${ENV} references, applies defaults, and validates.
func LoadReport(path string) (*ReportConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ReportConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse report config: %w", err)
	}
	dsn, err := interpolate(cfg.Database.DSN)
	if err != nil {
		return nil, fmt.Errorf("database dsn: %w", err)
	}
	cfg.Database.DSN = dsn
	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		pw, err := interpolate(s.Password)
		if err != nil {
			return nil, fmt.Errorf("server %s password: %w", s.Name, err)
		}
		s.Password = pw
		if s.PasswordFile != "" && s.Password == "" {
			b, err := os.ReadFile(s.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("server %s passwordFile: %w", s.Name, err)
			}
			s.Password = strings.TrimSpace(string(b))
		}
		if s.Tenant == "" {
			s.Tenant = s.Name
		}
	}
	if cfg.Capture.Interval == 0 {
		cfg.Capture.Interval = time.Hour
	}
	if cfg.Capture.Timeout == 0 {
		cfg.Capture.Timeout = 5 * time.Minute
	}
	if cfg.Capture.RetentionDays == 0 {
		cfg.Capture.RetentionDays = 400
	}
	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("database.dsn is required")
	}
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers configured")
	}
	return &cfg, nil
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(report): report config with env interpolation`

---

## Phase 2 — Capture DTOs

### Task 3: PPDM capture models

**Files:** Create `internal/report/models.go`, `internal/report/models_test.go`.

> Copy/policy fields are **provisional**. `parseTime` tolerates `""`/`null` → zero time.

- [ ] **Step 1: Write the failing test**

```go
package report

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	got, ok := parseTime("2026-06-05T01:04:12Z")
	if !ok || got.UTC() != time.Date(2026, 6, 5, 1, 4, 12, 0, time.UTC) {
		t.Fatalf("parseTime = %v ok=%v", got, ok)
	}
	if _, ok := parseTime(""); ok {
		t.Fatal("empty string should parse as not-ok")
	}
}

func TestJobAssetAndStatus(t *testing.T) {
	j := Job{State: "RUNNING"}
	if j.status() != "RUNNING" {
		t.Fatalf("status fallback = %q", j.status())
	}
	j.Result.Status = "SUCCESS"
	if j.status() != "SUCCESS" {
		t.Fatalf("status = %q", j.status())
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: parseTime`

- [ ] **Step 3: Implementation** — `internal/report/models.go`

```go
// Package report captures PPDM backup history into a durable store for assurance reporting.
package report

import "time"

// parseTime parses an RFC3339 timestamp, returning ok=false for empty/unparseable input.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Job is one /api/v2/activities record (a backup/restore job — immutable event).
type Job struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	Result      struct {
		Status           string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
	Asset struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"asset"` // provisional
	ProtectionPolicy struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"` // provisional
}

func (j Job) status() string {
	if j.Result.Status != "" {
		return j.Result.Status
	}
	return j.State
}

// Copy is one /api/v2/copies record (a backup copy with retention + location). Provisional.
type Copy struct {
	ID              string  `json:"id"`
	AssetID         string  `json:"assetId"`         // provisional
	PolicyName      string  `json:"policyName"`      // provisional
	CopyType        string  `json:"copyType"`        // provisional
	CreateTime      string  `json:"createTime"`      // provisional
	ExpirationTime  string  `json:"expirationTime"`  // provisional
	RetentionTime   string  `json:"retentionTime"`   // provisional
	RetentionLock   bool    `json:"retentionLock"`   // provisional
	StorageSystemID string  `json:"storageSystemId"` // provisional
	Location        string  `json:"location"`        // provisional
	Size            float64 `json:"size"`            // provisional
}

// Asset is one /api/v2/assets record (current protection state).
type Asset struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ProtectionStatus      string `json:"protectionStatus"`      // provisional
	LastAvailableCopyTime string `json:"lastAvailableCopyTime"` // provisional
	ProtectionPolicy      struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"` // provisional
}

// Policy is one /api/v3/protection-policies record. Objectives kept as raw JSON.
type Policy struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Objectives any    `json:"objectives"` // provisional; stored as jsonb
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(report): PPDM capture DTOs`

---

## Phase 3 — Store

### Task 4: Schema + Store.New + Migrate (testcontainers)

**Files:** Create `internal/report/migrations.sql`, `internal/report/store.go`, `internal/report/store_test.go`.

- [ ] **Step 1: Create `internal/report/migrations.sql`**

```sql
CREATE TABLE IF NOT EXISTS backup_jobs (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  category text,
  subcategory text,
  result_status text,
  asset_id text,
  asset_name text,
  policy_name text,
  started_at timestamptz,
  completed_at timestamptz,
  bytes_transferred bigint,
  created_at timestamptz,
  captured_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_backup_jobs_tenant_created ON backup_jobs (tenant, created_at);
CREATE INDEX IF NOT EXISTS idx_backup_jobs_server_created ON backup_jobs (server, created_at);

CREATE TABLE IF NOT EXISTS copies (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  asset_id text,
  policy_name text,
  copy_type text,
  create_time timestamptz,
  expiration_time timestamptz,
  retention_time timestamptz,
  retention_lock boolean,
  storage_system_id text,
  location text,
  size_bytes bigint,
  captured_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_copies_server_create ON copies (server, create_time);

CREATE TABLE IF NOT EXISTS assets (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  name text,
  type text,
  protection_status text,
  last_available_copy_time timestamptz,
  policy_name text,
  updated_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS protection_policies (
  id text PRIMARY KEY,
  tenant text NOT NULL,
  server text NOT NULL,
  name text,
  objectives jsonb,
  updated_at timestamptz NOT NULL,
  captured_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS capture_runs (
  id bigserial PRIMARY KEY,
  server text NOT NULL,
  started_at timestamptz NOT NULL,
  finished_at timestamptz,
  ok boolean NOT NULL DEFAULT false,
  error text,
  counts jsonb,
  tool_version text
);
```

- [ ] **Step 2: Write the failing test** — `internal/report/store_test.go`

```go
package report

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestStore spins up a throwaway Postgres and returns a migrated Store.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres testcontainers in -short mode")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("backup_report"),
		tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestMigrateCreatesTables(t *testing.T) {
	st := newTestStore(t)
	var n int
	err := st.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_name = ANY($1)`,
		[]string{"backup_jobs", "copies", "assets", "protection_policies", "capture_runs"},
	).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("created %d/5 tables", n)
	}
}
```

- [ ] **Step 3: Run — FAIL** `undefined: New` (`go test ./internal/report/ -run TestMigrate -v`)

- [ ] **Step 4: Implementation** — `internal/report/store.go`

```go
package report

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations.sql
var schemaSQL string

// Store is the PostgreSQL backup-history store.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool to dsn.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// Migrate applies the idempotent schema (CREATE TABLE IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schemaSQL)
	return err
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }
```

- [ ] **Step 5: Run — PASS** (Docker required; `go test ./internal/report/ -run TestMigrate -v`)

- [ ] **Step 6: Commit** `feat(report): Postgres store with embedded schema + migrate`

---

### Task 5: Idempotent upserts

**Files:** Modify `internal/report/store.go`; create `internal/report/upsert_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package report

import (
	"context"
	"testing"
	"time"
)

func TestUpsertJobsIdempotent(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	jobs := []Job{{ID: "j1", Category: "PROTECT", State: "COMPLETED",
		CreatedAt: "2026-06-05T01:00:00Z", StartedAt: "2026-06-05T01:00:00Z",
		CompletedAt: "2026-06-05T01:04:12Z"}}
	jobs[0].Result.Status = "SUCCESS"
	jobs[0].Result.BytesTransferred = 1048576
	jobs[0].Asset.Name = "vm-app01"

	for i := 0; i < 2; i++ { // upsert twice -> still one row
		if err := st.UpsertJobs(ctx, "acme", "ppdm01", jobs, now); err != nil {
			t.Fatalf("UpsertJobs: %v", err)
		}
	}
	var count int
	var status, asset string
	var bytes int64
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM backup_jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1 (idempotent)", count)
	}
	_ = st.pool.QueryRow(ctx, `SELECT result_status, asset_name, bytes_transferred FROM backup_jobs WHERE id='j1'`).
		Scan(&status, &asset, &bytes)
	if status != "SUCCESS" || asset != "vm-app01" || bytes != 1048576 {
		t.Fatalf("row = %s/%s/%d", status, asset, bytes)
	}
}

func TestUpsertCopiesAndAssets(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertCopies(ctx, "acme", "ppdm01", []Copy{{ID: "c1", AssetID: "a1",
		CopyType: "FULL", CreateTime: "2026-06-05T01:04:00Z", RetentionTime: "2026-07-05T01:04:00Z",
		RetentionLock: true, Location: "ddve-01", Size: 1048576}}, now); err != nil {
		t.Fatalf("UpsertCopies: %v", err)
	}
	if err := st.UpsertAssets(ctx, "acme", "ppdm01", []Asset{{ID: "a1", Name: "vm-app01",
		Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED",
		LastAvailableCopyTime: "2026-06-05T01:04:00Z"}}, now); err != nil {
		t.Fatalf("UpsertAssets: %v", err)
	}
	var lock bool
	_ = st.pool.QueryRow(ctx, `SELECT retention_lock FROM copies WHERE id='c1'`).Scan(&lock)
	if !lock {
		t.Fatal("retention_lock not stored")
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: UpsertJobs`

- [ ] **Step 3: Implementation** — append to `internal/report/store.go`

```go
import (
	// add to the existing import block:
	"time"

	"github.com/jackc/pgx/v5"
)

func ts(s string) *time.Time {
	if t, ok := parseTime(s); ok {
		return &t
	}
	return nil
}

// UpsertJobs inserts/updates backup_jobs by id (append-only events; re-capture is a no-op update).
func (s *Store) UpsertJobs(ctx context.Context, tenant, server string, jobs []Job, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, j := range jobs {
		b.Queue(`INSERT INTO backup_jobs
			(id,tenant,server,category,subcategory,result_status,asset_id,asset_name,policy_name,
			 started_at,completed_at,bytes_transferred,created_at,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (id) DO UPDATE SET result_status=EXCLUDED.result_status,
			 completed_at=EXCLUDED.completed_at, bytes_transferred=EXCLUDED.bytes_transferred,
			 captured_at=EXCLUDED.captured_at`,
			j.ID, tenant, server, j.Category, j.Subcategory, j.status(), j.Asset.ID, j.Asset.Name,
			j.ProtectionPolicy.Name, ts(j.StartedAt), ts(j.CompletedAt), int64(j.Result.BytesTransferred),
			ts(j.CreatedAt), capturedAt)
	}
	return s.sendBatch(ctx, b, len(jobs))
}

// UpsertCopies inserts/updates copies by id.
func (s *Store) UpsertCopies(ctx context.Context, tenant, server string, copies []Copy, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, c := range copies {
		b.Queue(`INSERT INTO copies
			(id,tenant,server,asset_id,policy_name,copy_type,create_time,expiration_time,retention_time,
			 retention_lock,storage_system_id,location,size_bytes,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (id) DO UPDATE SET expiration_time=EXCLUDED.expiration_time,
			 retention_time=EXCLUDED.retention_time, retention_lock=EXCLUDED.retention_lock,
			 captured_at=EXCLUDED.captured_at`,
			c.ID, tenant, server, c.AssetID, c.PolicyName, c.CopyType, ts(c.CreateTime),
			ts(c.ExpirationTime), ts(c.RetentionTime), c.RetentionLock, c.StorageSystemID,
			c.Location, int64(c.Size), capturedAt)
	}
	return s.sendBatch(ctx, b, len(copies))
}

// UpsertAssets upserts current asset protection state by id.
func (s *Store) UpsertAssets(ctx context.Context, tenant, server string, assets []Asset, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, a := range assets {
		b.Queue(`INSERT INTO assets
			(id,tenant,server,name,type,protection_status,last_available_copy_time,policy_name,updated_at,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO UPDATE SET protection_status=EXCLUDED.protection_status,
			 last_available_copy_time=EXCLUDED.last_available_copy_time, policy_name=EXCLUDED.policy_name,
			 updated_at=EXCLUDED.updated_at, captured_at=EXCLUDED.captured_at`,
			a.ID, tenant, server, a.Name, a.Type, a.ProtectionStatus, ts(a.LastAvailableCopyTime),
			a.ProtectionPolicy.Name, capturedAt, capturedAt)
	}
	return s.sendBatch(ctx, b, len(assets))
}

// UpsertPolicies upserts protection policies by id, objectives as jsonb.
func (s *Store) UpsertPolicies(ctx context.Context, tenant, server string, policies []Policy, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, p := range policies {
		b.Queue(`INSERT INTO protection_policies (id,tenant,server,name,objectives,updated_at,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, objectives=EXCLUDED.objectives,
			 updated_at=EXCLUDED.updated_at, captured_at=EXCLUDED.captured_at`,
			p.ID, tenant, server, p.Name, p.Objectives, capturedAt, capturedAt)
	}
	return s.sendBatch(ctx, b, len(policies))
}

func (s *Store) sendBatch(ctx context.Context, b *pgx.Batch, n int) error {
	if n == 0 {
		return nil
	}
	br := s.pool.SendBatch(ctx, b)
	defer br.Close()
	for i := 0; i < n; i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(report): idempotent upserts for jobs/copies/assets/policies`

---

### Task 6: Watermark + prune + RecordRun

**Files:** Modify `internal/report/store.go`; create `internal/report/watermark_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package report

import (
	"context"
	"testing"
	"time"
)

func TestWatermarkAndPrune(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	// One old job (beyond retention) and one recent.
	old := Job{ID: "old", Category: "PROTECT", CreatedAt: "2024-01-01T00:00:00Z"}
	recent := Job{ID: "new", Category: "PROTECT", CreatedAt: "2026-06-05T01:00:00Z"}
	if err := st.UpsertJobs(ctx, "acme", "ppdm01", []Job{old, recent}, now); err != nil {
		t.Fatal(err)
	}

	wm, err := st.JobWatermark(ctx, "ppdm01")
	if err != nil {
		t.Fatal(err)
	}
	if wm.UTC() != time.Date(2026, 6, 5, 1, 0, 0, 0, time.UTC) {
		t.Fatalf("watermark = %v, want 2026-06-05T01:00", wm)
	}

	if err := st.Prune(ctx, 400); err != nil { // ~13 months
		t.Fatal(err)
	}
	var count int
	_ = st.pool.QueryRow(ctx, `SELECT count(*) FROM backup_jobs`).Scan(&count)
	if count != 1 {
		t.Fatalf("after prune rows = %d, want 1 (old removed)", count)
	}
}

func TestRecordRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, err := st.StartRun(ctx, "ppdm01", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, id, true, "", map[string]int{"jobs": 3}); err != nil {
		t.Fatal(err)
	}
	var ok bool
	_ = st.pool.QueryRow(ctx, `SELECT ok FROM capture_runs WHERE id=$1`, id).Scan(&ok)
	if !ok {
		t.Fatal("run not marked ok")
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: JobWatermark`

- [ ] **Step 3: Implementation** — append to `internal/report/store.go`

```go
import (
	// add:
	"encoding/json"
)

// JobWatermark returns the newest created_at for a server's jobs, or zero time if none.
func (s *Store) JobWatermark(ctx context.Context, server string) (time.Time, error) {
	return s.watermark(ctx, `SELECT max(created_at) FROM backup_jobs WHERE server=$1`, server)
}

// CopyWatermark returns the newest create_time for a server's copies, or zero time if none.
func (s *Store) CopyWatermark(ctx context.Context, server string) (time.Time, error) {
	return s.watermark(ctx, `SELECT max(create_time) FROM copies WHERE server=$1`, server)
}

func (s *Store) watermark(ctx context.Context, q, server string) (time.Time, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, q, server).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// Prune deletes append-only rows older than retentionDays.
func (s *Store) Prune(ctx context.Context, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	if _, err := s.pool.Exec(ctx, `DELETE FROM backup_jobs WHERE created_at < $1`, cutoff); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM copies WHERE create_time < $1`, cutoff)
	return err
}

// StartRun opens a capture_runs row and returns its id.
func (s *Store) StartRun(ctx context.Context, server, version string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO capture_runs (server, started_at, tool_version) VALUES ($1, now(), $2) RETURNING id`,
		server, version).Scan(&id)
	return id, err
}

// FinishRun closes a capture_runs row with outcome + per-resource counts.
func (s *Store) FinishRun(ctx context.Context, id int64, ok bool, errMsg string, counts map[string]int) error {
	cj, _ := json.Marshal(counts)
	_, err := s.pool.Exec(ctx,
		`UPDATE capture_runs SET finished_at=now(), ok=$2, error=$3, counts=$4 WHERE id=$1`,
		id, ok, errMsg, cj)
	return err
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(report): watermark, retention prune, capture-run provenance`

---

## Phase 4 — Capture loop

### Task 7: Capturer.CaptureServer

**Files:** Create `internal/report/capture.go`, `internal/report/capture_test.go`.

Reuses `ppdmclient.GetAll[T]` and `ppdmclient.Mock` (no client change).

- [ ] **Step 1: Write the failing test** (mock client + real Postgres)

```go
package report

import (
	"context"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestCaptureServerPersists(t *testing.T) {
	st := newTestStore(t)
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", `{"page":{"totalPages":1},"content":[
		{"id":"j1","category":"PROTECT","state":"COMPLETED","createdAt":"2026-06-05T01:00:00Z",
		 "result":{"status":"SUCCESS","bytesTransferred":1048576},"asset":{"id":"a1","name":"vm-app01"}}]}`)
	m.SetJSONPrefix("/api/v2/copies", `{"page":{"totalPages":1},"content":[
		{"id":"c1","assetId":"a1","copyType":"FULL","createTime":"2026-06-05T01:04:00Z","retentionLock":true}]}`)
	m.SetJSONPrefix("/api/v2/assets", `{"page":{"totalPages":1},"content":[
		{"id":"a1","name":"vm-app01","type":"VMWARE_VIRTUAL_MACHINE","protectionStatus":"PROTECTED"}]}`)
	m.SetJSONPrefix("/api/v3/protection-policies", `{"page":{"totalPages":1},"content":[
		{"id":"p1","name":"Gold-VM","objectives":[{"type":"BACKUP"}]}]}`)

	cap := NewCapturer(st, "v-test", 400)
	if err := cap.CaptureServer(context.Background(), "acme", m); err != nil {
		t.Fatalf("CaptureServer: %v", err)
	}
	ctx := context.Background()
	for tbl, want := range map[string]int{"backup_jobs": 1, "copies": 1, "assets": 1, "protection_policies": 1} {
		var n int
		_ = st.pool.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n)
		if n != want {
			t.Errorf("%s rows = %d, want %d", tbl, n, want)
		}
	}
	var ok bool
	_ = st.pool.QueryRow(ctx, `SELECT ok FROM capture_runs WHERE server='ppdm01' ORDER BY id DESC LIMIT 1`).Scan(&ok)
	if !ok {
		t.Fatal("capture_run not ok")
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: NewCapturer`

- [ ] **Step 3: Implementation** — `internal/report/capture.go`

```go
package report

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	log "github.com/sirupsen/logrus"
)

// Capturer pulls authoritative PPDM records for one server and persists them.
type Capturer struct {
	store         *Store
	version       string
	retentionDays int
}

// NewCapturer wires a capturer to a store.
func NewCapturer(store *Store, version string, retentionDays int) *Capturer {
	return &Capturer{store: store, version: version, retentionDays: retentionDays}
}

// CaptureServer captures jobs/copies (incremental) + assets/policies (full) for one server,
// upserts them tagged with tenant, and records a capture_runs provenance row.
func (c *Capturer) CaptureServer(ctx context.Context, tenant string, client ppdmclient.Client) error {
	server := client.Name()
	runID, err := c.store.StartRun(ctx, server, c.version)
	if err != nil {
		return err
	}
	now := time.Now()
	counts, capErr := c.capture(ctx, tenant, server, client, now)
	msg := ""
	if capErr != nil {
		msg = capErr.Error()
		log.WithFields(log.Fields{"server": server, "err": capErr}).Warn("capture failed")
	}
	_ = c.store.FinishRun(ctx, runID, capErr == nil, msg, counts)
	return capErr
}

func (c *Capturer) capture(ctx context.Context, tenant, server string, client ppdmclient.Client, now time.Time) (map[string]int, error) {
	counts := map[string]int{}

	// Jobs (incremental by createdAt watermark; bootstrap to retention window on first run).
	jobWM, err := c.store.JobWatermark(ctx, server)
	if err != nil {
		return counts, err
	}
	jobs, err := ppdmclient.GetAll[Job](ctx, client, activitiesPath(c.bootstrap(jobWM)), 500)
	if err != nil {
		return counts, fmt.Errorf("activities: %w", err)
	}
	if err := c.store.UpsertJobs(ctx, tenant, server, jobs, now); err != nil {
		return counts, err
	}
	counts["jobs"] = len(jobs)

	// Copies (incremental by createTime watermark).
	copyWM, err := c.store.CopyWatermark(ctx, server)
	if err != nil {
		return counts, err
	}
	copies, err := ppdmclient.GetAll[Copy](ctx, client, copiesPath(c.bootstrap(copyWM)), 500)
	if err != nil {
		return counts, fmt.Errorf("copies: %w", err)
	}
	if err := c.store.UpsertCopies(ctx, tenant, server, copies, now); err != nil {
		return counts, err
	}
	counts["copies"] = len(copies)

	// Assets + policies (full state each cycle).
	assets, err := ppdmclient.GetAll[Asset](ctx, client, "/api/v2/assets", 500)
	if err != nil {
		return counts, fmt.Errorf("assets: %w", err)
	}
	if err := c.store.UpsertAssets(ctx, tenant, server, assets, now); err != nil {
		return counts, err
	}
	counts["assets"] = len(assets)

	policies, err := ppdmclient.GetAll[Policy](ctx, client, "/api/v3/protection-policies", 500)
	if err != nil {
		return counts, fmt.Errorf("policies: %w", err)
	}
	if err := c.store.UpsertPolicies(ctx, tenant, server, policies, now); err != nil {
		return counts, err
	}
	counts["policies"] = len(policies)
	return counts, nil
}

// bootstrap returns the watermark, or now-retention when there is no prior data, so the
// first capture backfills history without fetching the entire server.
func (c *Capturer) bootstrap(wm time.Time) time.Time {
	if wm.IsZero() {
		return time.Now().AddDate(0, 0, -c.retentionDays)
	}
	return wm
}

func activitiesPath(since time.Time) string {
	return "/api/v2/activities?filter=" + url.QueryEscape(`createdAt ge "`+since.UTC().Format(time.RFC3339)+`"`)
}

func copiesPath(since time.Time) string {
	return "/api/v2/copies?filter=" + url.QueryEscape(`createTime ge "`+since.UTC().Format(time.RFC3339)+`"`)
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(report): per-server capture (incremental jobs/copies, full assets/policies)`

---

### Task 8: Run loop + main.go wiring

**Files:** Modify `cmd/report/main.go`; create `config.report.yaml`. Add `Capturer.Run` to `internal/report/capture.go`.

- [ ] **Step 1: Add `Run` to `internal/report/capture.go`**

```go
import (
	// add:
	"golang.org/x/sync/errgroup"
)

// serverClient pairs a tenant with its client (set by main from config).
type serverClient struct {
	Tenant string
	Client ppdmclient.Client
}

// RunOnce captures every server once (in parallel) and prunes beyond retention.
func (c *Capturer) RunOnce(ctx context.Context, servers []serverClient, timeout time.Duration) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, sc := range servers {
		sc := sc
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, timeout)
			defer cancel()
			_ = c.CaptureServer(cctx, sc.Tenant, sc.Client) // errors recorded per server
			return nil
		})
	}
	_ = g.Wait()
	if err := c.store.Prune(ctx, c.retentionDays); err != nil {
		log.WithError(err).Warn("prune failed")
	}
}

// Run loops RunOnce on interval until ctx is cancelled.
func (c *Capturer) Run(ctx context.Context, servers []serverClient, interval, timeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.RunOnce(ctx, servers, timeout)
		}
	}
}
```

Export the pairing constructor so `main` can build it:

```go
// NewServerClient pairs a tenant with a PPDM client for RunOnce/Run.
func NewServerClient(tenant string, client ppdmclient.Client) serverClient {
	return serverClient{Tenant: tenant, Client: client}
}
```

- [ ] **Step 2: `config.report.yaml`**

```yaml
---
database:
  dsn: "postgres://report:${PG_PASSWORD}@localhost:5432/backup_report?sslmode=disable"
capture:
  interval: "1h"
  timeout: "5m"
  retentionDays: 400
servers:
  - name: ppdm-prod-01
    tenant: acme-corp
    host: ppdm01.example.com
    port: 8443
    username: ppdm-monitor
    password: "${PPDM1_PASSWORD}"
    insecureSkipVerify: true
```

- [ ] **Step 3: Replace `cmd/report/main.go`**

```go
// Command report captures PowerProtect Data Manager backup history into PostgreSQL
// for assurance reporting (durable history; Grafana + branded reports read it).
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	var cfgPath string
	var once, debug bool
	root := &cobra.Command{
		Use:     "report",
		Short:   "Capture PPDM backup history into PostgreSQL for assurance reporting",
		Version: version,
		RunE:    func(_ *cobra.Command, _ []string) error { return run(cfgPath, once, debug) },
	}
	root.Flags().StringVar(&cfgPath, "config", "config.report.yaml", "path to config file")
	root.Flags().BoolVar(&once, "once", false, "run a single capture cycle and exit")
	root.Flags().BoolVar(&debug, "debug", false, "verbose logging")
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(cfgPath string, once, debug bool) error {
	if debug {
		log.SetLevel(log.DebugLevel)
	}
	cfg, err := config.LoadReport(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := report.New(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	servers := make([]report.ServerClient, 0, len(cfg.Servers))
	var closers []ppdmclient.Client
	for _, s := range cfg.Servers {
		client := ppdmclient.NewServerClient(ppdmclient.Config{
			Name: s.Name, BaseURL: s.BaseURL(), Username: s.Username,
			Password: s.Password, InsecureSkipVerify: s.InsecureSkipVerify,
		})
		servers = append(servers, report.NewServerClient(s.Tenant, client))
		closers = append(closers, client)
	}
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	cap := report.NewCapturer(store, version, cfg.Capture.RetentionDays)
	log.Info("running initial capture cycle")
	cap.RunOnce(ctx, servers, cfg.Capture.Timeout)
	if once {
		return nil
	}
	cap.Run(ctx, servers, cfg.Capture.Interval, cfg.Capture.Timeout)
	return nil
}
```

> **Note:** rename the unexported `serverClient` type to exported `ServerClient` in
> `capture.go` (and `NewServerClient` returns `ServerClient`) so `main` can name the slice
> type. Update `RunOnce`/`Run` signatures to `[]ServerClient`.

- [ ] **Step 4: Build + vet**

Run: `make report-cli && go vet ./...`
Expected: builds; vet clean.

- [ ] **Step 5: Commit** `feat(report): capture loop + cmd/report wiring`

---

## Phase 5 — Demo stack

### Task 9: Postgres + report service + Grafana history view

**Files:** Modify `docker-compose.yml`, `cmd/mockppdm/main.go`, `Makefile`; create `cmd/mockppdm/fixtures/{copies,protection-policies}.json`, `deploy/report/Dockerfile`, `config.report.demo.yaml`, `grafana/provisioning/datasources/postgres.yml`, `grafana/dashboards/ppdm-backup-history.json`.

- [ ] **Step 1: Mock fixtures** — `cmd/mockppdm/fixtures/copies.json`

```json
{"page":{"number":0,"totalPages":1},"content":[
  {"id":"c-1","assetId":"v1","policyName":"Gold-VM","copyType":"FULL","createTime":"2026-06-05T01:04:00Z","retentionTime":"2026-07-05T01:04:00Z","retentionLock":true,"storageSystemId":"ddve-01","location":"ddve-01","size":1048576},
  {"id":"c-2","assetId":"f1","policyName":"FS-Weekly","copyType":"FULL","createTime":"2026-06-04T01:00:00Z","retentionTime":"2026-09-04T01:00:00Z","retentionLock":false,"storageSystemId":"ddve-01","location":"ddve-01","size":2148073472}
]}
```

`cmd/mockppdm/fixtures/protection-policies.json`:
```json
{"page":{"number":0,"totalPages":1},"content":[
  {"id":"p-1","name":"Gold-VM","objectives":[{"type":"BACKUP","schedule":{"interval":"PT24H"},"retention":{"interval":"P30D"}}]}
]}
```

- [ ] **Step 2: Register the two routes in `cmd/mockppdm/main.go`** — add to the `routes` map:

```go
	"/api/v2/copies":               "fixtures/copies.json",
	"/api/v3/protection-policies":  "fixtures/protection-policies.json",
```

- [ ] **Step 3: `deploy/report/Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/report ./cmd/report

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/report /report
USER nonroot:nonroot
ENTRYPOINT ["/report"]
CMD ["--config", "/etc/report/config.report.yaml"]
```

- [ ] **Step 4: `config.report.demo.yaml`**

```yaml
---
database:
  dsn: "postgres://report:report@postgres:5432/backup_report?sslmode=disable"
capture:
  interval: "30s"
  timeout: "30s"
  retentionDays: 400
servers:
  - name: mock-ppdm-01
    tenant: acme-corp
    host: mockppdm
    port: 8443
    username: demo
    password: demo
    insecureSkipVerify: true
```

- [ ] **Step 5: Add services to `docker-compose.yml`**

```yaml
  postgres:
    image: postgres:17-alpine
    container_name: ppdm_postgres
    environment:
      - POSTGRES_USER=report
      - POSTGRES_PASSWORD=report
      - POSTGRES_DB=backup_report
    ports:
      - "5432:5432"
    restart: unless-stopped

  report:
    build:
      context: .
      dockerfile: deploy/report/Dockerfile
    image: ppdm_report
    pull_policy: build
    container_name: ppdm_report
    command: ["--config", "/etc/report/config.report.yaml", "--debug"]
    volumes:
      - ./config.report.demo.yaml:/etc/report/config.report.yaml:ro
    depends_on:
      - mockppdm
      - postgres
    restart: unless-stopped
```

- [ ] **Step 6: Grafana Postgres datasource** — `grafana/provisioning/datasources/postgres.yml`

```yaml
apiVersion: 1
datasources:
  - name: BackupHistory
    type: postgres
    uid: backuphistory
    url: postgres:5432
    user: report
    jsonData: {database: backup_report, sslmode: disable}
    secureJsonData: {password: report}
    isDefault: false
    editable: false
```

- [ ] **Step 7: Grafana "Backup History" dashboard** — `grafana/dashboards/ppdm-backup-history.json`

```json
{
  "title": "PowerProtect — Backup History (global search)",
  "uid": "ppdm-backup-history",
  "tags": ["ppdm", "backup", "history"],
  "schemaVersion": 39, "version": 1, "time": {"from": "now-30d", "to": "now"},
  "templating": {"list": [
    {"name": "tenant", "type": "query", "datasource": {"type": "postgres", "uid": "backuphistory"},
     "query": "SELECT DISTINCT tenant FROM backup_jobs", "includeAll": true, "multi": true}
  ]},
  "panels": [
    {"id": 1, "type": "table", "title": "Backup jobs", "datasource": {"type": "postgres", "uid": "backuphistory"},
     "gridPos": {"h": 12, "w": 24, "x": 0, "y": 0},
     "targets": [{"refId": "A", "format": "table", "rawQuery": true,
       "rawSql": "SELECT created_at AS \"time\", tenant, server, category, result_status, asset_name, policy_name, bytes_transferred FROM backup_jobs WHERE tenant IN ($tenant) ORDER BY created_at DESC LIMIT 500"}]},
    {"id": 2, "type": "table", "title": "Copies (retention & location)", "datasource": {"type": "postgres", "uid": "backuphistory"},
     "gridPos": {"h": 10, "w": 24, "x": 0, "y": 12},
     "targets": [{"refId": "A", "format": "table", "rawQuery": true,
       "rawSql": "SELECT create_time AS \"time\", tenant, asset_id, copy_type, retention_time, retention_lock, location, size_bytes FROM copies WHERE tenant IN ($tenant) ORDER BY create_time DESC LIMIT 500"}]}
  ]
}
```

- [ ] **Step 8: Add `Makefile` demo note** — extend the existing `demo` target comment to mention Grafana now has *Backup History* (Postgres) alongside the metrics dashboards. No target change needed (compose `up` builds the new services).

- [ ] **Step 9: Verify end-to-end**

Run:
```bash
docker compose up --build -d
sleep 40
docker compose exec -T postgres psql -U report -d backup_report -c "SELECT count(*) FROM backup_jobs; SELECT count(*) FROM copies;"
```
Expected: non-zero counts (the mock's jobs + 2 copies captured). In Grafana, *Backup History* tables populate. `docker compose logs report` shows successful capture runs.

- [ ] **Step 10: Commit** `feat(report): docker-compose demo (postgres + capture + Grafana history)`

---

## Phase 6 — Docs

### Task 10: Docs

**Files:** Create `docs/report.md`; modify `CHANGELOG.md`, `CLAUDE.md`, `mkdocs.yml`.

- [ ] **Step 1: `docs/report.md`** — overview of `cmd/report`: purpose (durable backup history for assurance), the four captured resources, the Postgres schema, the config, and the Grafana *Backup History* view. Note this is Phase 1 of the reporter (SLA evaluation + branded report are later phases).

- [ ] **Step 2: `CLAUDE.md`** — add a short "cmd/report (backup history)" subsection under architecture: capture loop reusing `ppdmclient`, Postgres store, testcontainers DB tests, no exporter impact.

- [ ] **Step 3: `mkdocs.yml`** — add `- Backup history (cmd/report): report.md` to the nav.

- [ ] **Step 4: `CHANGELOG.md`** — `[Unreleased]` add: "cmd/report: durable PPDM backup-history capture into PostgreSQL (assurance reporting Phase 1)."

- [ ] **Step 5: Commit** `docs: cmd/report backup-history overview`

---

## Self-review notes (author)

- **Spec coverage:** capture loop (T7/T8) ↔ architecture; data model tables (T4) ↔ spec schema; incremental watermark + prune (T6) ↔ capture model; tenant tag on every table (T4) ↔ multi-tenancy-ready; provenance `capture_runs` (T6) ↔ lightweight provenance; Postgres + Grafana datasource (T9) ↔ global-search layer; testcontainers (T4–T7) ↔ test strategy; no client change (reuse `GetAll`) ↔ "shared client". De-scoped items (SLA, report, signing) are absent by design.
- **Type consistency:** `Store`, `New`, `Migrate`, `Upsert{Jobs,Copies,Assets,Policies}`, `JobWatermark`/`CopyWatermark`, `Prune`, `StartRun`/`FinishRun`, `Capturer`, `NewCapturer`, `CaptureServer`, `RunOnce`/`Run`, `ServerClient`/`NewServerClient`, `config.LoadReport`/`ReportConfig`/`ReportServer` used consistently across tasks. `ServerClient` is exported (T8 note corrects the T7 unexported draft).
- **No placeholders:** every code/test step is concrete; provisional PPDM fields flagged for ADR-0009.
- **Reuse:** `ppdmclient.{NewServerClient,Client,Mock,GetAll}`, `config.interpolate`/`envRef`, the loop/errgroup pattern, the compose/mock/grafana demo pattern — all from the existing exporter.
