# PPDM Exporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go Prometheus + OTLP exporter for Dell PowerProtect Data Manager (PPDM) that polls many PPDM servers on an interval, publishes an immutable snapshot, and serves it at `/metrics` while pushing the same snapshot over OTLP.

**Architecture:** The snapshot collection model (family standard). One background loop logs into every configured PPDM server, runs a set of modular per-domain `ResourceCollector`s in parallel, builds an immutable `Snapshot`, and pointer-swaps it into a `SnapshotStore`. Both export paths read the latest snapshot: an *unchecked* Prometheus collector on `/metrics` and OTLP observable gauges driven by a periodic reader. Mirrors the hand-rolled sibling `ppdd_exporter` for structure and `pflex_exporter` for the OTLP path.

**Tech Stack:** Go 1.26.4, `go-resty/resty/v2`, `prometheus/client_golang`, `go.opentelemetry.io/otel` (+ `otlpmetricgrpc`/`otlptracegrpc`), `spf13/cobra`, `sirupsen/logrus`, `fsnotify/fsnotify`, `gopkg.in/yaml.v2`, `golang.org/x/sync/errgroup`. Mock PPDM via `net/http/httptest`.

**Client decision (gating, ADR-0003):** **Hand-roll a lean `resty/v2` client.** No official Dell PPDM Go SDK exists (`dell/powerprotect-data-manager` ships Ansible/PowerShell automation enablers, not a Go library) — fails criterion (1) "available". This matches the sibling data-protection backends `ppdd` and `nbu`, both hand-rolled. Recorded in ADR-0003.

**Metric prefix / port:** `ppdm_` / **9102**. Identity label: **`server`** (one PPDM server per target).

> ⚠️ **Provisional API shapes.** PPDM endpoint paths and JSON field names below are modeled from the *PowerProtect Data Manager 19.22.0 REST API* PDF and **must be validated against a live PPDM server**. Each shape is confined to one module file; correcting one means editing one struct + one fixture. JSON tags are marked `// provisional` where unverified. The live-validation checklist at the end enumerates every shape to confirm.

---

## Reference siblings (read these before starting)

These exist locally and are the canonical templates. Where a task says "mirror sibling X", open that file and copy it, applying the named adaptations (module path → `github.com/fjacquet/ppdm_exporter`, package `ppdd`→`ppdm`, prefix `ppdd_`→`ppdm_`, identity label `system`→`server`, port `9099`→`9102`).

| Concern | Canonical sibling file |
|---|---|
| Whole-repo structure & this plan's format | `~/Projects/ppdd_exporter/` and its `docs/superpowers/plans/2026-05-30-ppdd-exporter.md` |
| Snapshot store, sample model, collector loop, prometheus collector, config, watcher | `~/Projects/ppdd_exporter/internal/ppdd/*.go`, `~/Projects/ppdd_exporter/internal/config/*.go` |
| Hand-rolled resty client + mock | `~/Projects/ppdd_exporter/internal/ddclient/*.go` |
| OTLP observable-gauge exporter | `~/Projects/pflex_exporter/internal/powerflex/otlp.go` |
| OTLP tracing manager | `~/Projects/pflex_exporter/internal/telemetry/manager.go` |
| Makefile / Dockerfile / CI trio / GoReleaser / dependabot | `~/Projects/ppdd_exporter/{Makefile,Dockerfile,.goreleaser.yaml,.github/**}` |
| ADR set | `~/Projects/ppdd_exporter/docs/adr/*.md` |

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Module `github.com/fjacquet/ppdm_exporter`, `go 1.26.4`, deps |
| `Makefile` | Full target contract (`tools fmt-check fmt vet lint test test-race test-coverage vuln ci sure cli sbom release release-snapshot docker run-cli clean`) |
| `main.go` | cobra CLI (`--config --debug --once`), wiring, HTTP server (before first collect), `/health`, OTLP start, reload |
| `config.yaml` | Example config |
| `internal/config/config.go` | Config struct, load, `${ENV}` interpolation, `passwordFile`, validation, defaults |
| `internal/config/watcher.go` | SIGHUP + fsnotify hot reload (rebuild-and-swap) |
| `internal/ppdmclient/client.go` | `Client` interface + resty `ServerClient` |
| `internal/ppdmclient/auth.go` | Bearer login (`POST /api/v2/login`), expiry-aware re-login, logout |
| `internal/ppdmclient/paginate.go` | `GetAll` — walks PPDM `{page,content}` envelopes |
| `internal/ppdmclient/mock.go` | In-memory `Client` for collector tests |
| `internal/ppdm/sample.go` | `Sample`/`Label` model + helpers (`WithServer`, `LabelValue`) |
| `internal/ppdm/snapshot.go` | `Snapshot`, `ServerSnapshot`, `SnapshotStore`, `MetricNames()`, `SamplesByName()` |
| `internal/ppdm/resource.go` | `ResourceCollector` interface + registry |
| `internal/ppdm/collector.go` | Background loop, `collectServer`, graceful degradation, `ppdm_up`/`ppdm_collector_up` |
| `internal/ppdm/activities.go` | Activities (jobs) collector |
| `internal/ppdm/assets.go` | Asset-protection collector |
| `internal/ppdm/capacity.go` | Capacity collector (datadomain-mtrees + storage-systems) |
| `internal/ppdm/health.go` | Health-entities + alerts collector |
| `internal/ppdm/prometheus.go` | Unchecked Prometheus collector over the snapshot |
| `internal/ppdm/otlp.go` | OTLP observable-gauge exporter over the snapshot |
| `internal/telemetry/manager.go` | Optional OTLP tracer provider lifecycle |
| `internal/ppdm/testdata/*.json` | Provisional fixtures |
| `Dockerfile`, `Dockerfile.goreleaser`, `.github/workflows/*`, `.goreleaser.yaml`, `.github/dependabot.yml` | Packaging, CI trio, release, supply-chain |
| `mkdocs.yml`, `docs/index.md`, `docs/getting-started/*`, `docs/deployment/*`, `docs/metrics.md`, `docs/adr/*` | MkDocs Material site + ADRs |
| `README.md`, `CLAUDE.md`, `LICENSE`, `CHANGELOG.md` | Project docs, conventions, license, changelog |

> **Standing convention (every task):** when a task adds/changes a user-visible feature, add a one-line entry under `CHANGELOG.md` `[Unreleased]` and include `CHANGELOG.md` in that task's `git add`.

---

## Phase 0 — Scaffolding

### Task 1: Module skeleton + Makefile + build

**Files:** Create `go.mod`, `Makefile`, `main.go`, `.gitignore`, `config.yaml`, `CHANGELOG.md`, `LICENSE`.

- [ ] **Step 1: `git init`** (repo is currently not under git)

Run: `cd ~/Projects/ppdm_exporter && git init`

- [ ] **Step 2: Create `go.mod`**

```
module github.com/fjacquet/ppdm_exporter

go 1.26.4
```

- [ ] **Step 3: Minimal `main.go` that builds**

```go
// Command ppdm_exporter is a Prometheus + OTLP exporter for Dell PowerProtect Data Manager.
package main

import "fmt"

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Printf("ppdm_exporter %s\n", version)
}
```

- [ ] **Step 4: Create the full-contract `Makefile`**

Copy `~/Projects/ppdd_exporter/Makefile`, then change `BIN := bin/ppdd_exporter` → `bin/ppdm_exporter` and any `ppdd_exporter` module references → `ppdm_exporter`. Verify it defines every target in the contract: `tools fmt-check fmt vet lint test test-race test-coverage vuln ci sure cli sbom release release-snapshot docker run-cli clean`. (ppdd is missing `docker`/`run-cli` per the drift table — **add them**:)

```makefile
docker:
	docker build -t ppdm_exporter:$(VERSION) .
run-cli: cli
	./bin/ppdm_exporter --config config.yaml --debug
```

- [ ] **Step 5: `CHANGELOG.md`** (Keep a Changelog + SemVer), `LICENSE` (copy ppdd's), `.gitignore` (copy ppdd's: `bin/`, `dist/`, `*.out`).

```markdown
# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Scaffold: Go module, full-contract Makefile, CLI skeleton.
```

- [ ] **Step 6: Build it**

Run: `go build ./... && go run .`
Expected: prints `ppdm_exporter dev`

- [ ] **Step 7: Commit**

```bash
git add go.mod Makefile main.go CHANGELOG.md LICENSE .gitignore
git commit -m "chore: scaffold ppdm_exporter module"
```

---

## Phase 1 — Core pipeline + activities (MVP)

### Task 2: Sample model

**Files:** Create `internal/ppdm/sample.go`, `internal/ppdm/sample_test.go`.

Mirror `~/Projects/ppdd_exporter/internal/ppdd/sample.go`, renaming package `ppdd`→`ppdm` and the helper `WithSystem`→`WithServer` (label key `system`→`server`).

- [ ] **Step 1: Failing test**

```go
package ppdm

import "testing"

func TestSampleLabelValueLookup(t *testing.T) {
	s := Sample{Name: "ppdm_asset_count", Value: 42,
		Labels: []Label{{Key: "server", Value: "ppdm01"}}}
	if got := s.LabelValue("server"); got != "ppdm01" {
		t.Fatalf("LabelValue(server) = %q, want ppdm01", got)
	}
	if got := s.LabelValue("missing"); got != "" {
		t.Fatalf("LabelValue(missing) = %q, want empty", got)
	}
}

func TestWithServerPrependsLabel(t *testing.T) {
	s := Sample{Name: "x", Labels: []Label{{Key: "category", Value: "PROTECT"}}}
	out := s.WithServer("ppdm01")
	if out.Labels[0].Key != "server" || out.Labels[0].Value != "ppdm01" {
		t.Fatalf("WithServer did not prepend server label: %+v", out.Labels)
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: Sample` (`go test ./internal/ppdm/ -run TestSample -v`)

- [ ] **Step 3: Implementation**

```go
// Package ppdm holds the PPDM metric model, snapshot store, modular collectors,
// and the Prometheus + OTLP export paths.
package ppdm

// Label is a single Prometheus label key/value.
type Label struct {
	Key   string
	Value string
}

// Sample is one metric data point: a name, an ordered label set, and a value.
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
}

// LabelValue returns the value of the named label, or "" if absent.
func (s Sample) LabelValue(key string) string {
	for _, l := range s.Labels {
		if l.Key == key {
			return l.Value
		}
	}
	return ""
}

// WithServer returns a copy with a leading {server=name} label. Collectors emit
// server-agnostic samples; the collection loop stamps the server identity.
func (s Sample) WithServer(name string) Sample {
	labels := make([]Label, 0, len(s.Labels)+1)
	labels = append(labels, Label{Key: "server", Value: name})
	labels = append(labels, s.Labels...)
	return Sample{Name: s.Name, Labels: labels, Value: s.Value}
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(ppdm): add Sample/Label metric model`

---

### Task 3: Snapshot store (+ OTLP helpers)

**Files:** Create `internal/ppdm/snapshot.go`, `internal/ppdm/snapshot_test.go`.

Mirror `~/Projects/ppdd_exporter/internal/ppdd/snapshot.go` (`SystemSnapshot`→`ServerSnapshot`, field `System`→`Server`), **and add the two OTLP helper methods** `MetricNames()` and `SamplesByName()` that pflex's OTLP path requires.

- [ ] **Step 1: Failing test**

```go
package ppdm

import (
	"testing"
	"time"
)

func TestSnapshotStoreLoadEmpty(t *testing.T) {
	st := NewSnapshotStore()
	if st.Load() == nil {
		t.Fatal("Load() on fresh store must return non-nil empty snapshot")
	}
	if n := len(st.Load().Servers); n != 0 {
		t.Fatalf("fresh snapshot has %d servers, want 0", n)
	}
}

func TestSnapshotMetricNamesAndSamplesByName(t *testing.T) {
	snap := &Snapshot{BuiltAt: time.Now(), Servers: []*ServerSnapshot{{
		Server: "ppdm01", OK: true,
		Samples: []Sample{
			{Name: "ppdm_asset_count", Labels: []Label{{"server", "ppdm01"}}, Value: 3},
			{Name: "ppdm_asset_count", Labels: []Label{{"server", "ppdm01"}}, Value: 5},
			{Name: "ppdm_up", Labels: []Label{{"server", "ppdm01"}}, Value: 1},
		},
	}}}
	st := NewSnapshotStore()
	st.Store(snap)
	names := st.Load().MetricNames()
	if len(names) != 2 { // deduped, sorted
		t.Fatalf("MetricNames = %v, want 2 distinct", names)
	}
	if got := st.Load().SamplesByName("ppdm_asset_count"); len(got) != 2 {
		t.Fatalf("SamplesByName(ppdm_asset_count) = %d, want 2", len(got))
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: NewSnapshotStore`

- [ ] **Step 3: Implementation**

```go
package ppdm

import (
	"sort"
	"sync"
	"time"
)

// ServerSnapshot is one PPDM server's result for a single collection cycle.
type ServerSnapshot struct {
	Server     string
	LastScrape time.Time
	OK         bool   // server reachable & authenticated
	Err        string // top-level failure (auth/unreachable); empty when OK
	Samples    []Sample
}

// Snapshot is an immutable, point-in-time view across all servers.
type Snapshot struct {
	BuiltAt time.Time
	Servers []*ServerSnapshot
}

// MetricNames returns the sorted, de-duplicated set of metric names in the snapshot.
func (s *Snapshot) MetricNames() []string {
	seen := map[string]struct{}{}
	for _, sv := range s.Servers {
		for _, sm := range sv.Samples {
			seen[sm.Name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SamplesByName returns every sample with the given metric name across all servers.
func (s *Snapshot) SamplesByName(name string) []Sample {
	var out []Sample
	for _, sv := range s.Servers {
		for _, sm := range sv.Samples {
			if sm.Name == name {
				out = append(out, sm)
			}
		}
	}
	return out
}

// SnapshotStore holds the latest Snapshot behind an RWMutex pointer-swap.
type SnapshotStore struct {
	mu   sync.RWMutex
	snap *Snapshot
}

// NewSnapshotStore returns a store pre-populated with an empty snapshot so readers
// never see nil before the first collection cycle.
func NewSnapshotStore() *SnapshotStore { return &SnapshotStore{snap: &Snapshot{}} }

// Store atomically swaps in a new snapshot.
func (s *SnapshotStore) Store(snap *Snapshot) { s.mu.Lock(); s.snap = snap; s.mu.Unlock() }

// Load returns the current snapshot (never nil).
func (s *SnapshotStore) Load() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(ppdm): immutable snapshot store with OTLP helpers`

---

### Task 4: ppdmclient interface + mock

**Files:** Create `internal/ppdmclient/client.go` (interface only), `internal/ppdmclient/mock.go`, `internal/ppdmclient/mock_test.go`.

Mirror `~/Projects/ppdd_exporter/internal/ddclient/{client.go,mock.go}`, renaming `ddclient`→`ppdmclient` and `system`→`server` wording.

- [ ] **Step 1: Failing test**

```go
package ppdmclient

import (
	"context"
	"testing"
)

func TestMockClientServesRegisteredPath(t *testing.T) {
	m := NewMock("ppdm01")
	m.SetJSON("/api/v2/activities", `{"content":[{"id":"a1"}],"page":{"totalPages":1}}`)

	var out struct {
		Content []struct {
			ID string `json:"id"`
		} `json:"content"`
	}
	if err := m.Get(context.Background(), "/api/v2/activities", &out); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if len(out.Content) != 1 || out.Content[0].ID != "a1" {
		t.Fatalf("decoded = %+v, want one activity a1", out.Content)
	}
}

func TestMockClientUnknownPathErrors(t *testing.T) {
	m := NewMock("ppdm01")
	var out map[string]any
	if err := m.Get(context.Background(), "/nope", &out); err == nil {
		t.Fatal("expected error for unregistered path")
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: NewMock`

- [ ] **Step 3: Interface + mock**

`internal/ppdmclient/client.go`:
```go
// Package ppdmclient is the per-server Dell PowerProtect Data Manager REST API client.
package ppdmclient

import "context"

// Client is the per-server PPDM API client abstraction, satisfied by the live
// ServerClient and by Mock (tests). Get authenticates lazily and decodes JSON.
type Client interface {
	// Name returns the configured server name (used as the `server` label).
	Name() string
	// Get fetches an absolute API path (e.g. "/api/v2/activities") and JSON-decodes
	// the body into out. It (re-)authenticates as needed.
	Get(ctx context.Context, path string, out any) error
	// Close releases the session and HTTP resources.
	Close() error
}
```

`internal/ppdmclient/mock.go`:
```go
package ppdmclient

import (
	"context"
	"encoding/json"
	"fmt"
)

// Mock is an in-memory Client that serves canned JSON bodies per path.
type Mock struct {
	name  string
	paths map[string]string
}

// NewMock returns an empty Mock for the named server.
func NewMock(name string) *Mock { return &Mock{name: name, paths: map[string]string{}} }

// SetJSON registers a response body for an exact path.
func (m *Mock) SetJSON(path, body string) { m.paths[path] = body }

func (m *Mock) Name() string { return m.name }

func (m *Mock) Get(_ context.Context, path string, out any) error {
	body, ok := m.paths[path]
	if !ok {
		return fmt.Errorf("mock: no response registered for %s", path)
	}
	return json.Unmarshal([]byte(body), out)
}

func (m *Mock) Close() error { return nil }
```

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(ppdmclient): Client interface and in-memory mock`

---

### Task 5: Live client + bearer auth (PPDM-specific)

**Files:** Create `internal/ppdmclient/auth.go`, `internal/ppdmclient/server.go`, `internal/ppdmclient/server_test.go`. Modify `go.mod` (resty).

> **PPDM auth (validated against the 19.22.0 PDF):** `POST /api/v2/login` with JSON body `{"username":..,"password":..}` returns JSON `{"access_token":..,"token_type":"Bearer","expires_in":1800,"refresh_token":..,"scope":"IAMScope"}`. Every subsequent request sends `Authorization: Bearer <access_token>`. The access token lives 1800s; this client re-logs-in when within 60s of expiry **and** on any 401. (`refresh_token` use is a future optimization — noted in ADR-0004.)

- [ ] **Step 1: Add resty** — `go get github.com/go-resty/resty/v2@latest`

- [ ] **Step 2: Failing test (httptest PPDM)**

```go
package ppdmclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// writeBytes avoids the Semgrep write-to-ResponseWriter rule.
func writeBytes(w http.ResponseWriter, b []byte) { _, _ = w.Write(b) }

func newFakePPDM(logins *int32, validToken string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(logins, 1)
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"access_token":"`+validToken+`","token_type":"Bearer","expires_in":1800,"refresh_token":"r1"}`))
	})
	mux.HandleFunc("/api/v2/activities", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+validToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"content":[{"id":"a1"}],"page":{"totalPages":1}}`))
	})
	return httptest.NewTLSServer(mux)
}

func TestServerClientAuthAndGet(t *testing.T) {
	var logins int32
	srv := newFakePPDM(&logins, "tok-123")
	defer srv.Close()

	c := NewServerClient(Config{
		Name: "ppdm01", BaseURL: srv.URL, Username: "u", Password: "p",
		InsecureSkipVerify: true, HTTPClient: srv.Client(),
	})
	defer c.Close()

	var out struct {
		Content []struct{ ID string `json:"id"` } `json:"content"`
	}
	if err := c.Get(context.Background(), "/api/v2/activities", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("content = %d, want 1", len(out.Content))
	}
	// Second Get reuses the token — no extra login.
	_ = c.Get(context.Background(), "/api/v2/activities", &out)
	if logins != 1 {
		t.Fatalf("logins = %d, want 1 (token reused)", logins)
	}
}
```

- [ ] **Step 3: Run — FAIL** `undefined: NewServerClient`

- [ ] **Step 4: Client + auth**

`internal/ppdmclient/server.go`:
```go
package ppdmclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

// Config configures a ServerClient. HTTPClient is optional (tests inject the
// httptest TLS client); when nil a client honoring InsecureSkipVerify is built.
type Config struct {
	Name               string
	BaseURL            string // e.g. https://ppdm01.example.com:8443
	Username           string
	Password           string
	InsecureSkipVerify bool
	HTTPClient         *http.Client
}

// ServerClient is the live per-server PPDM REST client.
type ServerClient struct {
	cfg Config
	rc  *resty.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewServerClient builds a client. Auth is lazy (on first Get).
func NewServerClient(cfg Config) *ServerClient {
	rc := resty.New().SetBaseURL(cfg.BaseURL)
	if cfg.HTTPClient != nil {
		rc.SetTransport(cfg.HTTPClient.Transport)
	} else {
		rc.SetTLSClientConfig(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // operator opt-in
		})
	}
	// Retry on 5xx only — never retry 4xx (auth/permission failures must not loop).
	rc.SetRetryCount(2).AddRetryCondition(func(r *resty.Response, _ error) bool {
		return r.StatusCode() >= 500
	})
	return &ServerClient{cfg: cfg, rc: rc}
}

func (c *ServerClient) Name() string { return c.cfg.Name }

// Get fetches path, authenticating first and re-authenticating once on 401.
func (c *ServerClient) Get(ctx context.Context, path string, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	resp, err := c.do(ctx, path, out)
	if err != nil {
		return err
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		c.clearToken()
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		if resp, err = c.do(ctx, path, out); err != nil {
			return err
		}
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode())
	}
	return nil
}

func (c *ServerClient) do(ctx context.Context, path string, out any) (*resty.Response, error) {
	return c.rc.R().SetContext(ctx).
		SetHeader("Authorization", "Bearer "+c.currentToken()).
		SetResult(out).
		Get(path)
}

func (c *ServerClient) currentToken() string { c.mu.Lock(); defer c.mu.Unlock(); return c.token }
func (c *ServerClient) clearToken()          { c.mu.Lock(); c.token = ""; c.mu.Unlock() }
```

`internal/ppdmclient/auth.go`:
```go
package ppdmclient

import (
	"context"
	"fmt"
	"time"
)

// loginResp is the PPDM login response (POST /api/v2/login). Validated against 19.22.0.
type loginResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// ensureToken logs in if there is no cached token or it is within 60s of expiry.
func (c *ServerClient) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	valid := c.token != "" && time.Now().Before(c.expiresAt.Add(-60*time.Second))
	c.mu.Unlock()
	if valid {
		return nil
	}

	var lr loginResp
	resp, err := c.rc.R().SetContext(ctx).
		SetBody(map[string]string{"username": c.cfg.Username, "password": c.cfg.Password}).
		SetResult(&lr).
		Post("/api/v2/login")
	if err != nil {
		return fmt.Errorf("login POST: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("login POST: status %d", resp.StatusCode())
	}
	if lr.AccessToken == "" {
		return fmt.Errorf("login POST: empty access_token in response")
	}
	ttl := lr.ExpiresIn
	if ttl <= 0 {
		ttl = 1800
	}
	c.mu.Lock()
	c.token = lr.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(ttl) * time.Second)
	c.mu.Unlock()
	return nil
}

// Close is best-effort logout. PPDM access tokens expire on their own; there is no
// mandatory server-side logout, so this just drops the cached token.
func (c *ServerClient) Close() error { c.clearToken(); return nil }
```

- [ ] **Step 5: Run — PASS** (`go test ./internal/ppdmclient/ -run TestServerClientAuth -v`)

- [ ] **Step 6: Add a re-login-on-401 test**

```go
func TestServerClientReloginOn401(t *testing.T) {
	var logins int32
	var rotated atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&logins, 1)
		tok := "tok1"
		if rotated.Load() {
			tok = "tok2"
		}
		writeBytes(w, []byte(`{"access_token":"`+tok+`","token_type":"Bearer","expires_in":1800}`))
	})
	mux.HandleFunc("/api/v2/activities", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok2" { // only tok2 accepted
			rotated.Store(true)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeBytes(w, []byte(`{"content":[],"page":{"totalPages":1}}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := NewServerClient(Config{Name: "ppdm01", BaseURL: srv.URL, HTTPClient: srv.Client()})
	defer c.Close()
	var out map[string]any
	if err := c.Get(context.Background(), "/api/v2/activities", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if logins != 2 {
		t.Fatalf("logins = %d, want 2 (initial + relogin)", logins)
	}
}
```

Run: `go test ./internal/ppdmclient/ -v` → PASS (all)

- [ ] **Step 7: Commit** `feat(ppdmclient): live bearer-auth client with relogin-on-401`

---

### Task 6: Pagination helper

**Files:** Create `internal/ppdmclient/paginate.go`, `internal/ppdmclient/paginate_test.go`.

> **PPDM pagination (validated):** list endpoints return `{"content":[...],"page":{"number":N,"size":S,"totalPages":T,"totalElements":E}}`. Pages are requested with `?page=<n>&pageSize=<s>` (0-indexed). `GetAll` walks pages and concatenates `content` into one decoded slice.

- [ ] **Step 1: Failing test**

```go
package ppdmclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAllWalksPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, []byte(`{"access_token":"t","token_type":"Bearer","expires_in":1800}`))
	})
	mux.HandleFunc("/api/v2/assets", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "0":
			writeBytes(w, []byte(`{"content":[{"id":"a"},{"id":"b"}],"page":{"number":0,"totalPages":2}}`))
		default:
			writeBytes(w, []byte(`{"content":[{"id":"c"}],"page":{"number":1,"totalPages":2}}`))
		}
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	c := NewServerClient(Config{Name: "ppdm01", BaseURL: srv.URL, HTTPClient: srv.Client()})
	defer c.Close()

	type asset struct{ ID string `json:"id"` }
	got, err := GetAll[asset](context.Background(), c, "/api/v2/assets", 500)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetAll returned %d items, want 3 across 2 pages", len(got))
	}
}
```

- [ ] **Step 2: Run — FAIL** `undefined: GetAll`

- [ ] **Step 3: Implementation**

```go
package ppdmclient

import (
	"context"
	"fmt"
)

// page is the PPDM pagination envelope.
type page[T any] struct {
	Content []T `json:"content"`
	Page    struct {
		Number     int `json:"number"`
		TotalPages int `json:"totalPages"`
	} `json:"page"`
}

// GetAll fetches every page of a PPDM list endpoint and returns the concatenated
// content. pageSize caps items per request; a 200-page safety bound prevents runaways.
func GetAll[T any](ctx context.Context, c Client, path string, pageSize int) ([]T, error) {
	var all []T
	for n := 0; n < 200; n++ {
		var p page[T]
		url := fmt.Sprintf("%s?page=%d&pageSize=%d", path, n, pageSize)
		if err := c.Get(ctx, url, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Content...)
		if p.Page.TotalPages <= 0 || n >= p.Page.TotalPages-1 {
			break
		}
	}
	return all, nil
}
```

> Note: the `Mock.Get` exact-path match must include the query string. In collector tests, register the mock under the full `"/api/v2/assets?page=0&pageSize=500"` key, or add a `Mock` mode that ignores the query — Task 7 uses the latter (see `SetJSONPrefix`). Add to `mock.go`:
> ```go
> // SetJSONPrefix registers a body matched by path prefix (ignores query string).
> func (m *Mock) SetJSONPrefix(prefix, body string) { m.prefixes = append(m.prefixes, kv{prefix, body}) }
> ```
> and have `Get` check `m.paths` first, then `m.prefixes` (by `strings.HasPrefix`). Add `type kv struct{ k, v string }` and `prefixes []kv` to `Mock`; init in `NewMock`. Update `TestMockClientServesRegisteredPath` remains green.

- [ ] **Step 4: Run — PASS.** **Step 5: Commit** `feat(ppdmclient): generic paginated GetAll over {page,content}`

---

### Task 7: Config model + env interpolation

**Files:** Create `internal/config/config.go`, `internal/config/config_test.go`. Modify `go.mod` (yaml.v2).

Mirror `~/Projects/ppdd_exporter/internal/config/config.go` with these adaptations: rename `System`→`Server`, default port `3009`→`8443`, default server port `9099`→`9102`, add `Collection.Lookback` (default `24h`, activities window) and `Collection.AssetAgeThreshold` (default `24h`, per-asset SLA-age emit cutoff), and an optional `OTel` block (endpoint/insecure/interval/enabled).

- [ ] **Step 1: Add yaml** — `go get gopkg.in/yaml.v2@latest`

- [ ] **Step 2: Failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInterpolatesEnvAndDefaults(t *testing.T) {
	t.Setenv("PPDM1_PASSWORD", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server: {host: "0.0.0.0", port: "9102", uri: "/metrics"}
collection: {interval: "5m", timeout: "60s", lookback: "24h"}
servers:
  - {name: ppdm01, host: ppdm01.example.com, username: u, password: "${PPDM1_PASSWORD}", insecureSkipVerify: true}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Servers[0].Password != "s3cret" {
		t.Fatalf("password = %q, want s3cret", cfg.Servers[0].Password)
	}
	if cfg.Servers[0].BaseURL() != "https://ppdm01.example.com:8443" {
		t.Fatalf("BaseURL = %q, want :8443 default", cfg.Servers[0].BaseURL())
	}
	if cfg.Collection.Lookback.String() != "24h0m0s" {
		t.Fatalf("lookback = %s, want 24h0m0s", cfg.Collection.Lookback)
	}
}

func TestLoadRejectsEmptyServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(path, []byte("servers: []\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when no servers configured")
	}
}
```

- [ ] **Step 3: Run — FAIL** `undefined: Load`

- [ ] **Step 4: Implementation** — copy ppdd's `config.go`, then apply the renames above. The PPDM-specific struct fields:

```go
// Server is one PPDM server to monitor.
type Server struct {
	Name               string `yaml:"name"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"` // defaults to 8443
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"passwordFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// BaseURL returns the https://host:port root for the PPDM REST API.
func (s Server) BaseURL() string {
	port := s.Port
	if port == 0 {
		port = 8443
	}
	return fmt.Sprintf("https://%s:%d", s.Host, port)
}

// Collection holds loop timing. Lookback bounds the activities query window;
// AssetAgeThreshold gates per-asset last-copy-age emission.
type Collection struct {
	Interval          time.Duration `yaml:"interval"`
	Timeout           time.Duration `yaml:"timeout"`
	Lookback          time.Duration `yaml:"lookback"`
	AssetAgeThreshold time.Duration `yaml:"assetAgeThreshold"`
}

// OTel configures optional OTLP metric/trace export.
type OTel struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
	Interval string `yaml:"interval"`
}

// Config is the whole file.
type Config struct {
	Server     ServerHTTP `yaml:"server"`
	Collection Collection `yaml:"collection"`
	OTel       OTel       `yaml:"otel"`
	Servers    []Server   `yaml:"servers"`
}
```

(Rename ppdd's HTTP-server struct to `ServerHTTP` to avoid colliding with the PPDM `Server` target struct. Defaults: server port `9102`, uri `/metrics`, interval `5m`, timeout `60s`, lookback `24h`, assetAgeThreshold `24h`. Validate `len(Servers) > 0`.)

- [ ] **Step 5: Run — PASS.** **Step 6: Commit** `feat(config): YAML config with env interpolation, PPDM defaults, OTel block`

---

### Task 8: ResourceCollector interface + activities collector

**Files:** Create `internal/ppdm/resource.go`, `internal/ppdm/activities.go`, `internal/ppdm/activities_test.go`, `internal/ppdm/testdata/activities.json`.

> **Activities shape (validated against 19.22.0 + Apache-2.0 module):** `GET /api/v2/activities` returns `{page,content[]}` where each activity has `category` (e.g. `PROTECT`, `RESTORE`, `DISCOVERY`, `REPLICATE`), `state` (`QUEUED`/`RUNNING`/`COMPLETED`/...), `result.status` (`SUCCESS`/`FAILED`/`OK`/...), and **`result.bytesTransferred`** (a *cumulative, summable* byte count — PDF-confirmed on `PROTECT` activities). To bound cardinality and load, the collector aggregates **counts by `category` × `result_status`** and **summed `result.bytesTransferred` by `category`**, over activities created within `collection.lookback`. The bytes total is a windowed sum recomputed each cycle → emitted as a gauge. `statistics.bytesTransferredPerSecond` is **not** used (summing instantaneous per-job rates is meaningless). The query is `GET /api/v2/activities?filter=createdAt ge "<RFC3339 since>"` (OData filter syntax confirmed by `dell.ppdm.psm1`).

- [ ] **Step 1: Provisional fixture `internal/ppdm/testdata/activities.json`**

```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"id":"1","category":"PROTECT","state":"COMPLETED","result":{"status":"SUCCESS","bytesTransferred":1048576}},
    {"id":"2","category":"PROTECT","state":"COMPLETED","result":{"status":"FAILED","bytesTransferred":0}},
    {"id":"3","category":"RESTORE","state":"RUNNING","result":{"status":"OK","bytesTransferred":524288}}
  ]
}
```

- [ ] **Step 2: Failing test**

```go
package ppdm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestActivitiesCollect(t *testing.T) {
	body, err := os.ReadFile("testdata/activities.json")
	if err != nil {
		t.Fatal(err)
	}
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", string(body))

	got, err := Activities{Lookback: 24 * time.Hour}.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	seen := map[string]float64{}
	for _, s := range got {
		key := s.Name + "|" + s.LabelValue("category") + "|" + s.LabelValue("result_status")
		seen[key] += s.Value
	}
	// 2 PROTECT activities: one SUCCESS, one FAILED.
	if seen["ppdm_activity_count|PROTECT|SUCCESS"] != 1 {
		t.Errorf("PROTECT/SUCCESS count = %v, want 1", seen["ppdm_activity_count|PROTECT|SUCCESS"])
	}
	if seen["ppdm_activity_count|PROTECT|FAILED"] != 1 {
		t.Errorf("PROTECT/FAILED count = %v, want 1", seen["ppdm_activity_count|PROTECT|FAILED"])
	}
	// Summed result.bytesTransferred for PROTECT = 1048576 + 0.
	if seen["ppdm_activity_bytes_total|PROTECT|"] != 1048576 {
		t.Errorf("PROTECT bytes total = %v, want 1048576", seen["ppdm_activity_bytes_total|PROTECT|"])
	}
}
```

- [ ] **Step 3: Run — FAIL** `undefined: Activities`

- [ ] **Step 4: `resource.go` + `activities.go`**

`internal/ppdm/resource.go`:
```go
package ppdm

import (
	"context"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// ResourceCollector collects one metric domain from a single PPDM server. It returns
// server-agnostic samples; the loop stamps the `server` label. Implementations own
// their endpoint path and JSON struct so provisional-API risk is localized.
type ResourceCollector interface {
	Name() string
	Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error)
}

// Registry is the ordered set of collectors run for every server.
func Registry(lookback, assetAgeThreshold time.Duration) []ResourceCollector {
	return []ResourceCollector{
		Activities{Lookback: lookback},
		Assets{AgeThreshold: assetAgeThreshold}, // Task 13
		Capacity{},                              // Task 14
		Health{},                                // Task 15
	}
}
```

`internal/ppdm/activities.go`:
```go
package ppdm

import (
	"context"
	"fmt"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// activity is the shape of one /api/v2/activities content item.
// state/result.status confirmed via the Apache-2.0 dell.ppdm.psm1; result.bytesTransferred
// is cumulative + summable (PDF-confirmed on PROTECT activities, line 15777).
type activity struct {
	Category string `json:"category"`
	State    string `json:"state"`
	Result   struct {
		Status         string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
}

// Activities aggregates PPDM job outcome counts and total bytes within a lookback window.
type Activities struct{ Lookback time.Duration }

func (Activities) Name() string { return "activities" }

func (a Activities) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	since := time.Now().Add(-a.Lookback).UTC().Format(time.RFC3339)
	path := fmt.Sprintf(`/api/v2/activities?filter=createdAt ge "%s"`, since)
	acts, err := ppdmclient.GetAll[activity](ctx, c, path, 500)
	if err != nil {
		return nil, err
	}

	type catKey struct{ category, status string }
	counts := map[catKey]float64{}
	bytesTotal := map[string]float64{} // cumulative result.bytesTransferred summed per category
	for _, act := range acts {
		status := act.Result.Status
		if status == "" {
			status = act.State // running/queued activities have no terminal status yet
		}
		counts[catKey{act.Category, status}]++
		bytesTotal[act.Category] += act.Result.BytesTransferred
	}

	var out []Sample
	for k, v := range counts {
		out = append(out, Sample{Name: "ppdm_activity_count", Value: v, Labels: []Label{
			{Key: "category", Value: k.category}, {Key: "result_status", Value: k.status},
		}})
	}
	for cat, v := range bytesTotal {
		out = append(out, Sample{Name: "ppdm_activity_bytes_total", Value: v,
			Labels: []Label{{Key: "category", Value: cat}, {Key: "result_status", Value: ""}}})
	}
	return out, nil
}
```

> **Label-key invariant:** every `ppdm_activity_*` series carries the same label-key set `{category, result_status}` in that order, with `result_status=""` where inapplicable. This is enforced by the Task 15 guard.

- [ ] **Step 5: Run — PASS.** **Step 6: Commit** `feat(ppdm): ResourceCollector interface + activities collector`

---

### Task 9: Collection loop + per-server/per-collector degradation

**Files:** Create `internal/ppdm/collector.go`, `internal/ppdm/collector_test.go`. Modify `go.mod` (errgroup, logrus).

Mirror `~/Projects/ppdd_exporter/internal/ppdd/collector.go`, adapting: `system`→`server`, emit `ppdm_collector_up{collector=...}` per collector and a top-level `ppdm_up` per server (1 when authenticated/reachable, 0 on a login failure that fails the whole server). `Registry()` now takes `lookback`.

- [ ] **Step 1: Add deps** — `go get golang.org/x/sync@latest github.com/sirupsen/logrus@latest`

- [ ] **Step 2: Failing test**

```go
package ppdm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestCollectOncePopulatesSnapshot(t *testing.T) {
	body, _ := os.ReadFile("testdata/activities.json")
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", string(body))

	store := NewSnapshotStore()
	col := NewCollector([]ppdmclient.Client{m}, []ResourceCollector{Activities{Lookback: time.Hour}}, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	if len(snap.Servers) != 1 {
		t.Fatalf("servers = %d, want 1", len(snap.Servers))
	}
	sv := snap.Servers[0]
	if !sv.OK {
		t.Fatalf("server not OK: %s", sv.Err)
	}
	var up float64
	for _, s := range sv.Samples {
		if s.Name == "ppdm_up" {
			up = s.Value
		}
		if s.LabelValue("server") != "ppdm01" {
			t.Errorf("sample %s missing server label", s.Name)
		}
	}
	if up != 1 {
		t.Fatalf("ppdm_up = %v, want 1", up)
	}
}

func TestCollectSystemDegradesOnError(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01") // no paths -> activities collector errors
	store := NewSnapshotStore()
	col := NewCollector([]ppdmclient.Client{m}, []ResourceCollector{Activities{Lookback: time.Hour}}, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	var collUp float64 = -1
	for _, s := range snap.Servers[0].Samples {
		if s.Name == "ppdm_collector_up" && s.LabelValue("collector") == "activities" {
			collUp = s.Value
		}
	}
	if collUp != 0 {
		t.Fatalf("ppdm_collector_up{activities} = %v, want 0", collUp)
	}
}
```

- [ ] **Step 3: Run — FAIL** `undefined: NewCollector`

- [ ] **Step 4: Implementation** — copy ppdd's `collector.go` and apply renames. Key shape (`collectServer`):

```go
func (c *Collector) collectServer(ctx context.Context, client ppdmclient.Client) *ServerSnapshot {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ss := &ServerSnapshot{Server: client.Name(), LastScrape: time.Now(), OK: true}
	serverUp := 1.0
	for _, rc := range c.collectors {
		samples, err := rc.Collect(ctx, client)
		up := 1.0
		if err != nil {
			up = 0
			serverUp = 0 // any collector failing marks the server degraded
			log.WithFields(log.Fields{"server": client.Name(), "collector": rc.Name(), "err": err}).
				Warn("collector failed")
		}
		ss.Samples = append(ss.Samples, Sample{
			Name: "ppdm_collector_up", Labels: []Label{{Key: "collector", Value: rc.Name()}}, Value: up,
		}.WithServer(client.Name()))
		for _, s := range samples {
			ss.Samples = append(ss.Samples, s.WithServer(client.Name()))
		}
	}
	if serverUp == 0 {
		ss.OK = false
		ss.Err = "one or more collectors failed"
	}
	ss.Samples = append(ss.Samples, Sample{Name: "ppdm_up", Value: serverUp}.WithServer(client.Name()))
	return ss
}
```

(The rest — `NewCollector`, `CollectOnce`, `Run` ticker loop, `collectAll` with `errgroup` — is identical to ppdd's, renamed.)

- [ ] **Step 5: Run — PASS.** **Step 6: Commit** `feat(ppdm): parallel collection loop with per-server/-collector degradation`

---

### Task 10: Prometheus collector

**Files:** Create `internal/ppdm/prometheus.go`, `internal/ppdm/prometheus_test.go`. Modify `go.mod` (client_golang).

Mirror `~/Projects/ppdd_exporter/internal/ppdd/prometheus.go` verbatim (rename package + help string `DD metric`→`PPDM metric`, field `Systems`→`Servers`). It is already an unchecked collector reading the snapshot.

- [ ] **Step 1: Add prometheus** — `go get github.com/prometheus/client_golang@latest`
- [ ] **Step 2: Failing test** (adapt ppdd's `TestPromCollectorEmitsSnapshot` — metric `ppdm_activity_count{server="ppdm01",category="PROTECT",result_status="SUCCESS"} 1`).
- [ ] **Step 3: Run — FAIL.** **Step 4: Implementation** (copy ppdd's `prometheus.go`, renamed). **Step 5: Run — PASS.**
- [ ] **Step 6: Commit** `feat(ppdm): unchecked Prometheus collector over snapshot`

---

### Task 11: main.go wiring + /health + flags (serve HTTP before first collect)

**Files:** Modify `main.go`; create `config.yaml`. Modify `go.mod` (cobra, promhttp).

Mirror `~/Projects/ppdd_exporter/main.go`. **Load-bearing adaptation:** the standard requires *serving HTTP before the first collection cycle* (`pstore` ADR-0007) — ppdd runs `CollectOnce` synchronously before `ListenAndServe`, which can stall `/metrics` on a slow first login. For PPDM, prime the store with the empty snapshot (already done by `NewSnapshotStore`), **start the HTTP server first**, then kick the first `CollectOnce` in a goroutine, then `Run`.

- [ ] **Step 1: Add cobra** — `go get github.com/spf13/cobra@latest github.com/prometheus/client_golang/prometheus/promhttp@latest`

- [ ] **Step 2: `config.yaml`**

```yaml
---
server:
  host: "0.0.0.0"
  port: "9102"
  uri: "/metrics"
collection:
  interval: "5m"
  timeout: "60s"
  lookback: "24h"
  assetAgeThreshold: "24h"
otel:
  enabled: false
  endpoint: "localhost:4317"
  insecure: true
  interval: "30s"
servers:
  - name: ppdm-prod-01
    host: ppdm01.example.com
    port: 8443
    username: ppdm-monitor
    password: "${PPDM1_PASSWORD}"
    insecureSkipVerify: true
```

- [ ] **Step 3: Replace `main.go`** — copy ppdd's `main.go` and apply: package doc, `ppdd`→`ppdm`, `ddclient`→`ppdmclient`, `NewSystemClient`→`NewServerClient`, `Registry()`→`Registry(cfg.Collection.Lookback, cfg.Collection.AssetAgeThreshold)`, `system`→`server`, default port string. Reorder so the server starts before the first collect:

```go
	store := ppdm.NewSnapshotStore()
	col := ppdm.NewCollector(clients, ppdm.Registry(cfg.Collection.Lookback, cfg.Collection.AssetAgeThreshold), store, cfg.Collection.Interval, cfg.Collection.Timeout)

	reg := prometheus.NewRegistry()
	reg.MustRegister(ppdm.NewPromCollector(store))
	mux := http.NewServeMux()
	mux.Handle(cfg.Server.URI, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { healthHandler(w, store) })
	srv := &http.Server{Addr: cfg.Server.Host + ":" + cfg.Server.Port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// Serve before the first collection cycle (slow first login must not block /metrics).
	go func() {
		log.WithField("addr", srv.Addr).Info("serving metrics")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(err)
		}
	}()

	log.Info("running initial collection cycle")
	col.CollectOnce(ctx)
	if once {
		_ = srv.Shutdown(context.Background())
		return nil
	}
	go col.Run(ctx)
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
```

(`healthHandler` is ppdd's, renamed `system`→`server`. OTLP start is added in Task 16.)

- [ ] **Step 4: Build + smoke-test**

Run: `make cli && ./bin/ppdm_exporter --once --config config.yaml --debug`
Expected: builds; one cycle; exits 0. Without a reachable PPDM the server reports `ppdm_up=0` / not OK — expected. Validate wiring, not live data.

- [ ] **Step 5: Commit** `feat: wire CLI, loop, /metrics, /health; serve before first collect`

---

### Task 12: Hot config reload (SIGHUP + file watch)

**Files:** Create `internal/config/watcher.go`, `internal/config/watcher_test.go`. Modify `go.mod` (fsnotify), `main.go`.

Mirror `~/Projects/ppdd_exporter/internal/config/watcher.go` and its test verbatim (no PPDM specifics). Wire `main.go` to rebuild clients + collector on each `w.Updates()` (rebuild-and-swap, ADR-0005).

- [ ] Steps 1–6: copy ppdd's `watcher.go`, `watcher_test.go`, run FAIL→PASS, wire main.go, commit `feat(config): SIGHUP + fsnotify hot reload`.

---

### Phase 1 gate

- [ ] `make ci` green (fmt, vet, golangci-lint, `go test -race`, govulncheck).
- [ ] `CHANGELOG.md [Unreleased]` reflects: scaffold, sample, snapshot, client+auth, pagination, config, activities, loop, prometheus, wiring, reload.

---

## Phase 2 — Asset-protection collector

### Task 13: Assets collector

**Files:** Create `internal/ppdm/assets.go`, `internal/ppdm/assets_test.go`, `internal/ppdm/testdata/assets.json`.

> **Assets shape:** `GET /api/v2/assets` returns `{page,content[]}`; each asset has `type` (e.g. `VMWARE_VIRTUAL_MACHINE`, `FILE_SYSTEM`, `KUBERNETES`) — **confirmed** via the Apache-2.0 `assetmgmt.py` — plus `protectionStatus` (`PROTECTED`/`UNPROTECTED`/`LAPSED` — **provisional** enum) and `lastAvailableCopyTime` (RFC3339 or null — **provisional**). The collector emits **counts by `type` × `protection_status`** + an `ppdm_asset_unprotected` rollup (cheap, fleet-independent), **plus** a bounded per-asset `ppdm_asset_last_copy_age_seconds` (Q1-C): emitted only for an asset that has a parseable `lastAvailableCopyTime` **and** is either non-`PROTECTED` or older than `AgeThreshold`. Healthy on-time assets emit no per-asset series; never-copied assets surface only in the rollups. Emitted cardinality tracks the problem count, not the fleet size.

- [ ] **Step 1: Fixture `testdata/assets.json`** — one healthy (suppressed), one unprotected/never-copied (rollup only), one stale `PROTECTED` (age emitted). Timestamps are relative to the test's injected `now = 2026-06-05T00:00:00Z`.

```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"id":"v1","name":"vm-app01","type":"VMWARE_VIRTUAL_MACHINE","protectionStatus":"PROTECTED","lastAvailableCopyTime":"2026-06-04T20:00:00Z"},
    {"id":"v2","name":"vm-app02","type":"VMWARE_VIRTUAL_MACHINE","protectionStatus":"UNPROTECTED","lastAvailableCopyTime":null},
    {"id":"f1","name":"nas01","type":"FILE_SYSTEM","protectionStatus":"PROTECTED","lastAvailableCopyTime":"2026-06-01T00:00:00Z"}
  ]
}
```

- [ ] **Step 2: Failing test** — rollups as before, plus: only the stale `PROTECTED` asset (`nas01`, 4 days old) emits an age series; the healthy `vm-app01` (4h old, under the 24h threshold) does not.

```go
func TestAssetsCollect(t *testing.T) {
	body, _ := os.ReadFile("testdata/assets.json")
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/assets", string(body))
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	a := Assets{AgeThreshold: 24 * time.Hour, now: func() time.Time { return now }}
	got, err := a.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	seen := map[string]float64{}
	for _, s := range got {
		seen[s.Name+"|"+s.LabelValue("type")+"|"+s.LabelValue("protection_status")] += s.Value
	}
	if seen["ppdm_asset_count|VMWARE_VIRTUAL_MACHINE|PROTECTED"] != 1 {
		t.Errorf("VM/PROTECTED = %v, want 1", seen["ppdm_asset_count|VMWARE_VIRTUAL_MACHINE|PROTECTED"])
	}
	if seen["ppdm_asset_unprotected||"] != 1 {
		t.Errorf("unprotected rollup = %v, want 1", seen["ppdm_asset_unprotected||"])
	}
	// Bounded SLA series: only nas01 (stale PROTECTED) appears; vm-app01 (healthy) is suppressed.
	var ageAssets []string
	for _, s := range got {
		if s.Name == "ppdm_asset_last_copy_age_seconds" {
			ageAssets = append(ageAssets, s.LabelValue("asset"))
			if s.LabelValue("asset") == "nas01" && s.Value != 4*86400 {
				t.Errorf("nas01 age = %v, want %v", s.Value, 4*86400)
			}
		}
	}
	if len(ageAssets) != 1 || ageAssets[0] != "nas01" {
		t.Errorf("age series assets = %v, want [nas01] only", ageAssets)
	}
}
```

- [ ] **Step 3: Run — FAIL.** **Step 4: Implementation**

```go
package ppdm

import (
	"context"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

type asset struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`                  // confirmed (assetmgmt.py)
	ProtectionStatus      string `json:"protectionStatus"`      // provisional enum
	LastAvailableCopyTime string `json:"lastAvailableCopyTime"` // provisional; RFC3339 or "" (null)
}

// Assets aggregates protection rollups plus a bounded per-asset SLA-age series.
// AgeThreshold gates the per-asset emission; now is injectable for tests (defaults to time.Now).
type Assets struct {
	AgeThreshold time.Duration
	now          func() time.Time
}

func (Assets) Name() string { return "assets" }

func (a Assets) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a Assets) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	assets, err := ppdmclient.GetAll[asset](ctx, c, "/api/v2/assets", 500)
	if err != nil {
		return nil, err
	}
	now := a.clock()
	type k struct{ typ, status string }
	counts := map[k]float64{}
	var unprotected float64
	var ageSamples []Sample
	for _, as := range assets {
		counts[k{as.Type, as.ProtectionStatus}]++
		if as.ProtectionStatus != "PROTECTED" {
			unprotected++
		}
		if as.LastAvailableCopyTime == "" {
			continue // never-copied: surfaced by the rollups, no age to report
		}
		t, perr := time.Parse(time.RFC3339, as.LastAvailableCopyTime)
		if perr != nil {
			continue
		}
		age := now.Sub(t).Seconds()
		if as.ProtectionStatus != "PROTECTED" || age > a.AgeThreshold.Seconds() {
			ageSamples = append(ageSamples, Sample{Name: "ppdm_asset_last_copy_age_seconds", Value: age,
				Labels: []Label{
					{Key: "asset", Value: as.Name},
					{Key: "type", Value: as.Type},
					{Key: "protection_status", Value: as.ProtectionStatus},
				}})
		}
	}
	out := []Sample{{Name: "ppdm_asset_unprotected", Value: unprotected,
		Labels: []Label{{Key: "type", Value: ""}, {Key: "protection_status", Value: ""}}}}
	for key, v := range counts {
		out = append(out, Sample{Name: "ppdm_asset_count", Value: v, Labels: []Label{
			{Key: "type", Value: key.typ}, {Key: "protection_status", Value: key.status},
		}})
	}
	return append(out, ageSamples...), nil
}
```

(`ppdm_asset_count`/`ppdm_asset_unprotected` carry `{type, protection_status}`; `ppdm_asset_last_copy_age_seconds` is a distinct name with its own fixed key set `{asset, type, protection_status}` — each name is internally consistent, satisfying the invariant.)

- [ ] **Step 5: Run — PASS.** **Step 6: Commit** `feat(ppdm): asset-protection collector`

---

## Phase 3 — Capacity collector

### Task 14: Capacity collector (datadomain-mtrees + storage-systems)

**Files:** Create `internal/ppdm/capacity.go`, `internal/ppdm/capacity_test.go`, `internal/ppdm/testdata/{datadomain-mtrees,storage-systems}.json`.

> **Capacity shapes (provisional — validate against live PPDM):** `GET /api/v2/datadomain-mtrees` returns `{page,content[]}`; each storage-unit (MTree) reports a logical/physical size. Field names vary by PPDM build — model as `name`, `physicalCapacityBytes`, `physicalUsedBytes`, `logicalUsedBytes`. `GET /api/v2/storage-systems` returns `{page,content[]}` with `name`, `type`, and a capacity block. **These two are the most likely to need correction on a live system** — both are isolated here.

- [ ] **Step 1: Fixtures** `testdata/datadomain-mtrees.json` and `testdata/storage-systems.json`:

```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"name":"/data/col1/su-policy-a","physicalCapacityBytes":3220957036544,"physicalUsedBytes":342523641856,"logicalUsedBytes":1099511627776}
  ]
}
```
```json
{
  "page": {"number": 0, "totalPages": 1},
  "content": [
    {"name":"ddve-01","type":"DATA_DOMAIN_SYSTEM","totalSizeBytes":3220957036544,"usedSizeBytes":342523641856}
  ]
}
```

- [ ] **Step 2: Failing test** — assert `ppdm_storage_unit_physical_used_bytes{storage_unit="/data/col1/su-policy-a"} == 342523641856`, `ppdm_storage_unit_physical_capacity_bytes{...} == 3220957036544`, `ppdm_storage_unit_logical_used_bytes{...} == 1099511627776`, and `ppdm_storage_system_used_bytes{storage_system="ddve-01",type="DATA_DOMAIN_SYSTEM"} == 342523641856`, `ppdm_storage_system_total_bytes{...} == 3220957036544`.

- [ ] **Step 3: Run — FAIL.** **Step 4: Implementation**

```go
package ppdm

import (
	"context"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

type mtree struct {
	Name          string  `json:"name"`                  // provisional
	PhysicalCap   float64 `json:"physicalCapacityBytes"` // provisional
	PhysicalUsed  float64 `json:"physicalUsedBytes"`     // provisional
	LogicalUsed   float64 `json:"logicalUsedBytes"`      // provisional
}

type storageSystem struct {
	Name      string  `json:"name"`           // provisional
	Type      string  `json:"type"`           // provisional
	TotalSize float64 `json:"totalSizeBytes"` // provisional
	UsedSize  float64 `json:"usedSizeBytes"`  // provisional
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
			Sample{Name: "ppdm_storage_unit_physical_capacity_bytes", Value: m.PhysicalCap, Labels: su},
			Sample{Name: "ppdm_storage_unit_physical_used_bytes", Value: m.PhysicalUsed, Labels: su},
			Sample{Name: "ppdm_storage_unit_logical_used_bytes", Value: m.LogicalUsed, Labels: su},
		)
	}
	for _, s := range systems {
		sl := []Label{{Key: "storage_system", Value: s.Name}, {Key: "type", Value: s.Type}}
		out = append(out,
			Sample{Name: "ppdm_storage_system_total_bytes", Value: s.TotalSize, Labels: sl},
			Sample{Name: "ppdm_storage_system_used_bytes", Value: s.UsedSize, Labels: sl},
		)
	}
	return out, nil
}
```

- [ ] **Step 5: Run — PASS.** **Step 6: Commit** `feat(ppdm): capacity collector (mtrees + storage-systems)`

---

## Phase 4 — Health & alerts collector + invariant guard

### Task 15: Health collector (health-entities + alerts)

**Files:** Create `internal/ppdm/health.go`, `internal/ppdm/health_test.go`, `internal/ppdm/testdata/{health-entities,alerts}.json`.

> **Shapes:** `GET /api/v3/health-entities` returns `{page,content[]}`; each entity has `name`/`component` and a `status` (provisional enum — `OK`/`WARNING`/`ERROR`/...; the PDF confirms a `componentType` field but leaves the status enum unexpanded → **provisional**). `GET /api/v2/alerts` returns `{page,content[]}`; each alert has `severity` (**PDF-confirmed** enum `CRITICAL`/`WARNING`/`INFORMATIONAL`), `category`, and `acknowledgement.acknowledgeState` (a **string** enum — the PDF leaves `acknowledgement` as an opaque `{}`; the Apache-2.0 `dell.ppdm.psm1`/`Example-03.ps1` confirms the `acknowledgeState` field name; its exact values stay provisional). The collector emits `ppdm_health_entity_status{entity,component}` (1 when `OK`, else 0) and `ppdm_alert_count{severity,ack_state}` aggregated counts. **Note:** `ack_state` replaced an earlier boolean `acknowledged` after mining the official repo.

- [ ] **Step 1: Fixtures** `testdata/health-entities.json`, `testdata/alerts.json`:

```json
{"page":{"number":0,"totalPages":1},"content":[
  {"name":"ppdm-server","component":"SYSTEM","status":"OK"},
  {"name":"dd-storage","component":"STORAGE","status":"WARNING"}
]}
```
```json
{"page":{"number":0,"totalPages":1},"content":[
  {"severity":"CRITICAL","category":"PROTECT","acknowledgement":{"acknowledgeState":"NOT_ACKNOWLEDGED"}},
  {"severity":"WARNING","category":"SYSTEM","acknowledgement":{"acknowledgeState":"ACKNOWLEDGED"}},
  {"severity":"CRITICAL","category":"SYSTEM","acknowledgement":{"acknowledgeState":"NOT_ACKNOWLEDGED"}}
]}
```

(Enum values `NOT_ACKNOWLEDGED`/`ACKNOWLEDGED` are provisional — confirm against a live server, ADR-0009.)

- [ ] **Step 2: Failing test** — assert `ppdm_health_entity_status{entity="ppdm-server",component="SYSTEM"} == 1`, `...{entity="dd-storage",component="STORAGE"} == 0`, `ppdm_alert_count{severity="CRITICAL",ack_state="NOT_ACKNOWLEDGED"} == 2`, `ppdm_alert_count{severity="WARNING",ack_state="ACKNOWLEDGED"} == 1`.

- [ ] **Step 3: Run — FAIL.** **Step 4: Implementation**

```go
package ppdm

import (
	"context"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

type healthEntity struct {
	Name      string `json:"name"`      // provisional
	Component string `json:"component"` // provisional
	Status    string `json:"status"`    // provisional (enum unconfirmed)
}

type alert struct {
	Severity        string `json:"severity"` // PDF-confirmed: CRITICAL/WARNING/INFORMATIONAL
	Acknowledgement struct {
		// acknowledgeState confirmed by the Apache-2.0 dell.ppdm.psm1; enum values provisional.
		AcknowledgeState string `json:"acknowledgeState"`
	} `json:"acknowledgement"`
}

// Health collects PPDM health-entity status and alert counts.
type Health struct{}

func (Health) Name() string { return "health" }

func (Health) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	entities, err := ppdmclient.GetAll[healthEntity](ctx, c, "/api/v3/health-entities", 500)
	if err != nil {
		return nil, err
	}
	alerts, err := ppdmclient.GetAll[alert](ctx, c, "/api/v2/alerts", 500)
	if err != nil {
		return nil, err
	}
	var out []Sample
	for _, e := range entities {
		v := 0.0
		if e.Status == "OK" {
			v = 1
		}
		out = append(out, Sample{Name: "ppdm_health_entity_status", Value: v, Labels: []Label{
			{Key: "entity", Value: e.Name}, {Key: "component", Value: e.Component},
		}})
	}
	type ak struct{ severity, ackState string }
	counts := map[ak]float64{}
	for _, a := range alerts {
		counts[ak{a.Severity, a.Acknowledgement.AcknowledgeState}]++
	}
	for k, v := range counts {
		out = append(out, Sample{Name: "ppdm_alert_count", Value: v, Labels: []Label{
			{Key: "severity", Value: k.severity}, {Key: "ack_state", Value: k.ackState},
		}})
	}
	return out, nil
}
```

- [ ] **Step 5: Run — PASS.** **Step 6: Commit** `feat(ppdm): health-entities + alerts collector`

---

### Task 16: Label-key consistency guard (load-bearing, ADR-0006)

**Files:** Create `internal/ppdm/labels_test.go`.

> The invariant (architecture.md): a metric name must carry **one** label-key set across all its series. This test runs every collector against its fixture, stamps the `server` label, and asserts that for each metric name the ordered label-key set is identical across all samples. It is the regression guard for the empty-value-for-inapplicable-keys discipline used by `ppdm_activity_*` and `ppdm_asset_*`.

- [ ] **Step 1: Write the test**

```go
package ppdm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func keySet(s Sample) string {
	keys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		keys[i] = l.Key
	}
	return strings.Join(keys, ",")
}

func TestLabelKeySetConsistentPerMetric(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	for _, f := range []struct{ prefix, file string }{
		{"/api/v2/activities", "testdata/activities.json"},
		{"/api/v2/assets", "testdata/assets.json"},
		{"/api/v2/datadomain-mtrees", "testdata/datadomain-mtrees.json"},
		{"/api/v2/storage-systems", "testdata/storage-systems.json"},
		{"/api/v3/health-entities", "testdata/health-entities.json"},
		{"/api/v2/alerts", "testdata/alerts.json"},
	} {
		body, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		m.SetJSONPrefix(f.prefix, string(body))
	}

	collectors := []ResourceCollector{Activities{Lookback: time.Hour}, Assets{}, Capacity{}, Health{}}
	want := map[string]string{}
	for _, rc := range collectors {
		samples, err := rc.Collect(context.Background(), m)
		if err != nil {
			t.Fatalf("%s.Collect: %v", rc.Name(), err)
		}
		for _, s := range samples {
			s = s.WithServer("ppdm01")
			ks := keySet(s)
			if prev, ok := want[s.Name]; ok && prev != ks {
				t.Errorf("metric %s has inconsistent label keys: %q vs %q", s.Name, prev, ks)
			}
			want[s.Name] = ks
		}
	}
}
```

- [ ] **Step 2: Run — PASS** (if any collector violates the invariant, fix the collector by padding the union label set, not the test). **Step 3: Commit** `test(ppdm): label-key consistency guard`

---

## Phase 5 — OTLP dual export

### Task 17: OTLP observable-gauge exporter

**Files:** Create `internal/ppdm/otlp.go`, `internal/ppdm/otlp_test.go`. Modify `go.mod` (otel sdk + otlpmetricgrpc), `main.go`.

Mirror `~/Projects/pflex_exporter/internal/powerflex/otlp.go`, adapting: package `ppdm`, `SnapshotStore` from this repo, `attrsFor` reads `Label.Key`/`Label.Value` (this repo's `Label` uses `Key`, not pflex's `Name`), service name `ppdm-exporter`. The exporter registers one `Float64ObservableGauge` per metric name from `snap.MetricNames()`; each callback observes `store.Load().SamplesByName(name)`.

- [ ] **Step 1: Add otel** — `go get go.opentelemetry.io/otel@latest go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@latest go.opentelemetry.io/otel/sdk@latest`

- [ ] **Step 2: Failing test (ManualReader — assert dual-path parity)**

```go
package ppdm

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestOTLPObservesSnapshot(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{BuiltAt: time.Now(), Servers: []*ServerSnapshot{{
		Server: "ppdm01", OK: true,
		Samples: []Sample{{Name: "ppdm_alert_count",
			Labels: []Label{{"server", "ppdm01"}, {"severity", "CRITICAL"}, {"ack_state", "NOT_ACKNOWLEDGED"}}, Value: 2}},
	}}})
	reader := metric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	if err := exp.EnsureInstruments(); err != nil {
		t.Fatalf("EnsureInstruments: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "ppdm_alert_count" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("ppdm_alert_count not observed via OTLP ManualReader")
	}
}
```

- [ ] **Step 3: Run — FAIL** `undefined: newOTLPExporter`

- [ ] **Step 4: Implementation** — copy pflex's `otlp.go`, change `attrsFor`:

```go
func attrsFor(labels []Label) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, len(labels))
	for i, l := range labels {
		attrs[i] = attribute.String(l.Key, l.Value)
	}
	return attrs
}
```

and `OTLPExporter.store` is `*SnapshotStore`, service name `ppdm-exporter`. (`NewOTLPExporter(ctx, endpoint, insecure, interval, store, version)` builds the periodic reader; `newOTLPExporter(reader, store, version)` is the testable seam.)

- [ ] **Step 5: Run — PASS.** **Step 6: Wire `main.go`** — when `cfg.OTel.Enabled`, build `NewOTLPExporter`, call `EnsureInstruments()` after the first `CollectOnce` and again after each reload, `defer exp.Shutdown(ctx)`.

- [ ] **Step 7: Commit** `feat(ppdm): OTLP observable-gauge dual export`

### Task 18: OTLP tracing manager (optional spans)

**Files:** Create `internal/telemetry/manager.go`. Mirror `~/Projects/pflex_exporter/internal/telemetry/manager.go` verbatim (service name `ppdm-exporter`); wire in `main.go` behind `cfg.OTel.Enabled`. Commit `feat(telemetry): optional OTLP tracer provider`.

### Phase 5 gate

- [ ] Both export paths tested: Prometheus registry gather (Task 10) **and** OTLP `ManualReader` (Task 17) over the same snapshot.
- [ ] `make ci` green.

---

## Phase 6 — Packaging, CI/CD, docs, ADRs

### Task 19: Dockerfile (non-root) + Dockerfile.goreleaser

Mirror `~/Projects/ppdd_exporter/Dockerfile` (multi-stage; non-root `USER`; expose `9102`) and create `Dockerfile.goreleaser` (copies prebuilt binary). `make docker` builds it. Commit `chore: multi-stage non-root Dockerfile`.

### Task 20: CI trio + GoReleaser + dependabot (SHA-pinned)

- [ ] Copy `~/Projects/ppdd_exporter/.github/workflows/{ci.yml,release.yml,docs.yml}`, `.goreleaser.yaml`, `.github/dependabot.yml`. Adapt names (`ppdd`→`ppdm`), Homebrew cask name, GHCR image path, port. **Keep every action SHA-pinned with explicit `# vX.Y.Z`** (resolve any drifted comments via `gh api repos/<owner>/<action>/commits/<tag> --jq .sha`).
- [ ] `.goreleaser.yaml` `version: 2`: `CGO_ENABLED=0`, `goos:[linux,darwin]`, `goarch:[amd64,arm64]`, `-trimpath`, `ldflags -s -w -X main.version={{.Version}}`, `mod_timestamp`, cyclonedx-gomod SBOM, `checksums.txt`, self-skipping Homebrew cask.
- [ ] `goreleaser check` passes; `make release-snapshot` dry-run succeeds.
- [ ] Set GitHub Pages deployment to `workflow`.
- [ ] Commit `ci: add ci/release/docs workflows, GoReleaser, dependabot (SHA-pinned)`.

### Task 21: MkDocs site + metrics catalog + README + CLAUDE.md

- [ ] Copy ppdd's `mkdocs.yml` + `docs/` tree; rewrite for PPDM. Author `docs/metrics.md` cataloguing every metric: `ppdm_up`, `ppdm_collector_up`, `ppdm_activity_count`, `ppdm_activity_bytes_total`, `ppdm_asset_count`, `ppdm_asset_unprotected`, `ppdm_asset_last_copy_age_seconds`, `ppdm_storage_unit_{physical_capacity,physical_used,logical_used}_bytes`, `ppdm_storage_system_{total,used}_bytes`, `ppdm_health_entity_status`, `ppdm_alert_count` — with labels (e.g. `ppdm_alert_count{server,severity,ack_state}`), units, and a note that `ppdm_activity_bytes_total` is a windowed sum (gauge), aggregate with `sum`/`max`, never `rate()`.
- [ ] Write `CLAUDE.md` (overview, commands, architecture, load-bearing constraints, testing, CI/CD) and `README.md`.
- [ ] Commit `docs: MkDocs site, metrics catalog, README, CLAUDE.md`.

### Task 22: ADRs

**Files:** `docs/adr/index.md` + `NNNN-title.md`. Reuse ppdd's ADRs as templates (Status/Context/Decision/Consequences). Required set:

- [ ] `0001-ci-supply-chain-hardening.md` (SHA-pinning, SBOM, govulncheck) — from ppdd 0008.
- [ ] `0002-prometheus-snapshot-model.md` — from ppdd 0001.
- [ ] `0003-handrolled-resty-client.md` — **PPDM-specific:** no official Dell PPDM Go SDK (criterion 1 "available" fails); the official `dell/powerprotect-data-manager` repo is Apache-2.0 automation enablers (PowerShell module + Python), not a Go library. Mirrors `ppdd`/`nbu`. Cite the client rule.
- [ ] `0004-bearer-auth-retry-policy.md` — `POST /api/v2/login`, bearer header, expiry-aware re-login + relogin-on-401, retry excludes 4xx; note `refresh_token` deferred.
- [ ] `0005-config-hot-reload.md` — from ppdd 0005.
- [ ] `0006-label-key-consistency-invariant.md` — from ppdd 0006; cite Task 16 guard.
- [ ] `0007-metric-naming-and-units.md` — `ppdm_` prefix, `server` identity label, per-second-as-gauge.
- [ ] `0008-serve-http-before-first-collect.md` — from pstore 0007; cite Task 11.
- [ ] `0009-provisional-api-mappings.md` — enumerate provisional shapes as the **two-bucket** checklist (below): validated-by-reference (PDF + Apache-2.0 `dell.ppdm.psm1`) vs still-unconfirmed. Cite the official repo (Apache-2.0) as a validation source and credit it for the `acknowledgeState` correction.
- [ ] Commit `docs(adr): record PPDM exporter decisions 0001-0009`.

### Task 23: First release

- [ ] Tag `v0.1.0`; confirm `release.yml` produces binaries + SBOM + GHCR image. Commit/tag.

---

## Post-implementation: live-validation checklist (ADR-0009)

Shapes are confirmed against two reference sources where possible (the 19.22.0 PDF and the Apache-2.0 `dell/powerprotect-data-manager` PowerShell module), leaving a shorter list that genuinely needs a live server. Each shape lives in exactly one file.

**Bucket A — validated-by-reference** (PDF and/or the Apache-2.0 module; verify opportunistically, low risk):

- [x] **auth** (`ppdmclient/auth.go`): `POST /api/v2/login` body/`access_token`/Bearer — confirmed by `Python/secure_login_helper.py` + `dell.ppdm.psm1`. (Still verify `expires_in` numeric on a live server.)
- [x] **pagination** (`ppdmclient/paginate.go`): `?pageSize=` + `{content,page}` — confirmed by `dell.ppdm.psm1` (Example-01/02/15).
- [x] **activities** (`ppdm/activities.go`): `state`/`result.status` + OData `filter=... ge "..."` — confirmed by `Example-02.ps1`/`adhocbck.py`.
- [x] **alerts.severity** (`ppdm/health.go`): enum `CRITICAL`/`WARNING`/`INFORMATIONAL` — PDF-confirmed.
- [x] **assets.type** (`ppdm/assets.go`): confirmed by `assetmgmt.py`.

**Bucket B — still unconfirmed by every available source** (the real live-validation list, **highest priority**):

- [ ] **capacity** (`ppdm/capacity.go`): MTree + storage-system capacity field names. Unconfirmed by the repo *and* ambiguous in the PDF (only VM-disk/datastore capacity examples surface). **Highest correction risk.** Consider sourcing authoritative DD capacity from the sibling `ppdd_exporter` instead.
- [ ] **health-entities.status** (`ppdm/health.go`): the status/health enum (PDF confirms `componentType` only).
- [ ] **alerts.acknowledgeState** (`ppdm/health.go`): the field name comes from the Apache-2.0 module; its **enum values** (`ACKNOWLEDGED`/`NOT_ACKNOWLEDGED`/...) are unverified.
- [ ] **assets** (`ppdm/assets.go`): `protectionStatus` enum + `lastAvailableCopyTime` presence/format.
- [ ] **activities.result.bytesTransferred** (`ppdm/activities.go`): confirm it's cumulative + present across categories (seen on `PROTECT` in the PDF).

When a field is wrong: fix the one struct + one fixture, rerun that collector's test. No other file changes.

---

## Self-review notes (author)

- **Spec coverage:** all four chosen families have a collector (activities T8, assets T13, capacity T14, health+alerts T15); dual export T17; identity label `server` stamped in the loop T9; serve-before-collect T11; hot reload T12; label invariant T16; full CI/Make/Docker/ADR universal layer in Phase 6.
- **Type consistency:** `ServerSnapshot.Server`, `Snapshot.Servers`, `Sample.WithServer`, `Label.Key`, `ppdmclient.Client.Get`, `Registry(lookback)`, `NewServerClient` used consistently across tasks. `SetJSONPrefix` introduced in T6 and used by all collector tests.
- **No placeholders:** PPDM-specific files (auth, paginate, the four collectors, OTLP `attrsFor`, snapshot OTLP helpers, the invariant guard) carry full code; mechanically-identical scaffolding references the exact sibling file to copy with named adaptations.
