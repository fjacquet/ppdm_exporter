# /health always-200 + /livez /readyz (ppdm_exporter) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `/livez`/`/readyz` (always-200, no state) and make `/health`
always answer 200, matching obs_exporter's ADR-0013/ADR-0014 pattern.

**Architecture:** New `staticOKHandler` registered at `/livez`/`/readyz`.
`healthHandler` (`main.go:197-219`) loses its `StatusServiceUnavailable`
branch. JSON body shape (`built_at`, `servers: [{server, ok, last_scrape, err}]`)
unchanged.

**Tech Stack:** Go, `net/http`, `net/http/httptest`.

## Global Constraints

- Repo: `/Users/fjacquet/Projects/ppdm_exporter`.
- Spec: `/Users/fjacquet/Projects/obs_exporter/docs/superpowers/specs/2026-08-01-family-health-endpoint-design.md` (bucket B).
- `/health`'s path and JSON shape do not change — only the status code. Not a breaking change.
- `/livez`/`/readyz` are net-new — `### Added` in CHANGELOG.
- Next ADR number: 0014. ADR index table has 2 columns (`ADR | Title`) — match that.
- `internal/ppdm/health_test.go` exists but tests an unrelated `Health.Collect` metric collector, not this HTTP handler — leave it untouched.
- No `main_test.go` exists in this repo's root package today — this plan creates it.

---

### Task 1: `/livez` `/readyz` + drop `/health`'s 503

**Files:**
- Modify: `main.go:86` (add two `mux.HandleFunc` lines after the existing `/health` registration)
- Modify: `main.go:197-219` (function `healthHandler`, remove 503 branch)
- Create: `main.go` — add `staticOKHandler` function after `healthHandler`'s closing brace
- Create: `main_test.go`

**Interfaces:**
- Consumes: `ppdm.SnapshotStore` (`internal/ppdm/snapshot.go:54`) — `Load() *Snapshot`, `NewSnapshotStore() *SnapshotStore`. `ppdm.Snapshot` (`internal/ppdm/snapshot.go:19-22`): `BuiltAt time.Time`, `Servers []*ServerSnapshot`. `ppdm.ServerSnapshot` (`internal/ppdm/snapshot.go:10-16`): `Server string`, `LastScrape time.Time`, `OK bool`, `Err string`, `Samples []Sample`.
- Produces: `staticOKHandler(w http.ResponseWriter, _ *http.Request)`.

- [ ] **Step 1: Write failing tests**

Create `main_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdm"
)

func TestLivezReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	staticOKHandler(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyzReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	staticOKHandler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHealthReturns200WhenServerUnhealthy(t *testing.T) {
	store := ppdm.NewSnapshotStore()
	store.Store(&ppdm.Snapshot{
		BuiltAt: time.Now(),
		Servers: []*ppdm.ServerSnapshot{
			{Server: "ppdm01", OK: false, Err: "login POST: status 401"},
		},
	})

	rec := httptest.NewRecorder()
	healthHandler(rec, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Servers []struct {
			Server string `json:"server"`
			OK     bool   `json:"ok"`
			Err    string `json:"err"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Servers) != 1 || body.Servers[0].OK {
		t.Fatalf("servers = %+v, want one server with ok=false", body.Servers)
	}
}

func TestHealthReturns200WhenNoServers(t *testing.T) {
	store := ppdm.NewSnapshotStore()

	rec := httptest.NewRecorder()
	healthHandler(rec, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestLivezReturnsOK|TestReadyzReturnsOK|TestHealthReturns200' -v`
Expected: `TestLivezReturnsOK`/`TestReadyzReturnsOK` FAIL with `undefined: staticOKHandler`. `TestHealthReturns200*` FAIL with `status = 503, want 200`.

- [ ] **Step 3: Add `staticOKHandler` and register `/livez` `/readyz`**

In `main.go`, change line 86 from:

```go
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { healthHandler(w, store) })
```

to:

```go
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { healthHandler(w, store) })
	mux.HandleFunc("/livez", staticOKHandler)
	mux.HandleFunc("/readyz", staticOKHandler)
```

After `healthHandler`'s closing brace (currently line 219), add:

```go

// staticOKHandler always answers 200 — no collection state, nothing that
// can make it fail. /livez and /readyz both use it: a probe wired here can
// never be the reason a healthy process gets restarted or pulled from
// rotation.
func staticOKHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Drop the 503 branch in `healthHandler`**

Change the end of `healthHandler` (`main.go:197-219`) from:

```go
	healthy := len(snap.Servers) > 0
	for _, s := range snap.Servers {
		out.Servers = append(out.Servers, serverHealth{s.Server, s.OK, s.LastScrape.Format(time.RFC3339), s.Err})
		if !s.OK {
			healthy = false
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(out)
```

to:

```go
	for _, s := range snap.Servers {
		out.Servers = append(out.Servers, serverHealth{s.Server, s.OK, s.LastScrape.Format(time.RFC3339), s.Err})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test . -run 'TestLivezReturnsOK|TestReadyzReturnsOK|TestHealthReturns200' -v`
Expected: all PASS.

- [ ] **Step 6: Run full test suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add main.go main_test.go
git commit -m "feat: add /livez /readyz, /health always answers 200

Matches obs_exporter's ADR-0013/ADR-0014 pattern: a server being
unreachable is data the exporter reports, not a failure of the
exporter itself. /livez and /readyz are trivial always-200 probe
endpoints with no state read; /health's JSON body (built_at,
servers[]) is unchanged, only its status code stops varying."
```

---

### Task 2: Chart, ADR, docs, CHANGELOG

**Files:**
- Modify: `charts/ppdm-exporter/values.yaml:50-56`
- Create: `docs/adr/0014-health-always-200-and-static-probes.md`
- Modify: `docs/adr/index.md` (append row after 0013, 2-column format `| ADR | Title |`)
- Modify: `CHANGELOG.md` (under existing `## [Unreleased]`)
- Modify: any deployment/troubleshooting docs mentioning `/health` and probe wiring — grep first (see Step 1)

**Interfaces:**
- Consumes: nothing (docs-only task).
- Produces: nothing.

- [ ] **Step 1: Find every doc mentioning `/health` as a probe target**

Run: `grep -rn '/health\|livenessProbe\|readinessProbe' docs/ README.md 2>/dev/null`

Update every hit describing `/health` as what the chart's probes use, or as
ever returning non-200: probes now use `/livez`/`/readyz` (always 200, no
server state); `/health` always answers 200 too, JSON body's `ok`/`err` per
server is the status signal. Use obs_exporter's `docs/deployment/kubernetes.md`
and `docs/operate/troubleshooting.md` (`~/Projects/obs_exporter/`) as the
structural template if this repo lacks comparable depth.

- [ ] **Step 2: Update the chart**

In `charts/ppdm-exporter/values.yaml:50-56`, change:

```yaml
livenessProbe:
  httpGet:
    path: /health
readinessProbe:
  httpGet:
    path: /health
```

to:

```yaml
livenessProbe:
  httpGet:
    path: /livez
readinessProbe:
  httpGet:
    path: /readyz
```

(Only the `path:` values change — keep every other key in that block as-is.)

- [ ] **Step 3: Write ADR-0014**

Create `docs/adr/0014-health-always-200-and-static-probes.md`:

```markdown
# `/livez` `/readyz`, and `/health` always answering 200

## Status

Accepted (2026-08-01). Additive. Does not supersede any prior ADR.

## Context

Same argument as obs_exporter's ADR-0013 and ADR-0014, applied here in one
pass: an exporter is a probe. "Server unreachable" is data it reports, not a
failure of the exporter process. Coupling that fact to an HTTP status code
on any endpoint — the chart's `livenessProbe`/`readinessProbe`, or the
informational `/health` — risks something downstream (kubelet, a dashboard,
a script) treating a healthy, correctly-reporting exporter as down.

`charts/ppdm-exporter/values.yaml` wired both `livenessProbe` and
`readinessProbe` to `/health`, which answered 503 while any configured
server was unreachable. As a *liveness* check this is always wrong: no
restart makes an unreachable server reachable. As a *readiness* check it
pulls the exporter from the scrape pool exactly when the down-server metric
is the fact worth scraping.

## Decision

Two new endpoints, `/livez` and `/readyz`, both `staticOKHandler` — always
`200 OK`, no `SnapshotStore` read, nothing that can make either fail once
the process is running. The chart's default probes now point at them.
`/health`'s `healthHandler` no longer writes `http.StatusServiceUnavailable`
— it always answers 200, with the same JSON body (`built_at`,
`servers: [{server, ok, last_scrape, err}]`) as before. The per-server
`ok`/`err` fields are the only status channel now; nothing that parses the
body loses information.

## Consequences

- Anything gating on `/health`'s HTTP status code now sees 200
  unconditionally and must read `ok`/`err` per server instead.
- Chart default probe wiring changes; a fresh `helm install` or an upgrade
  without pinned probe overrides gets the fix automatically.
- Alert on a per-server `_up` metric (or `/health`'s body), never on any
  probe's HTTP status.
```

- [ ] **Step 4: Add the ADR to the index**

In `docs/adr/index.md`, after the `0013` row, add (2-column format):

```markdown
| [0014](0014-health-always-200-and-static-probes.md) | `/livez`/`/readyz` static probes; `/health` always answers 200 |
```

- [ ] **Step 5: CHANGELOG entry**

In `CHANGELOG.md`, under the existing `## [Unreleased]` heading, add:

```markdown
### Added

- `/livez` and `/readyz`: probe endpoints that always answer 200, with no
  dependency on server reachability or the collection cycle. See ADR-0014.

### Changed

- `/health` always answers 200, never 503. The JSON body's per-server
  `ok`/`err` fields are unchanged and remain the way to tell whether a
  server is degraded — read the body, not the status code. See ADR-0014.
  Not a breaking change: the path and JSON shape are unchanged.
- The chart's default `livenessProbe`/`readinessProbe` now point at
  `/livez`/`/readyz` instead of `/health`.
```

- [ ] **Step 6: Lint chart + build docs**

Run: `helm lint charts/ppdm-exporter` (or the exact CI invocation from `.github/workflows/` if different)
Expected: exits 0.

Run: `mkdocs build --strict` (if `mkdocs.yml` present)
Expected: exits 0.

- [ ] **Step 7: Commit**

```bash
git add charts/ppdm-exporter/values.yaml docs/adr/0014-health-always-200-and-static-probes.md \
  docs/adr/index.md CHANGELOG.md
git commit -m "docs+chart: record ADR-0014, repoint chart probes to /livez /readyz"
```
