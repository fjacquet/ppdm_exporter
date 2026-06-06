# Backup Reporter Phase 4b — Per-Tenant /report Access Control — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scope each `/report` bearer token to specific tenant(s) so a token for tenant A cannot read tenant B's report, replacing the single all-tenants global token.

**Architecture:** A config `report.tokens` list (token → tenant scopes) is mapped in `cmd/report` into a `render.Authorizer` (sha256-keyed registry). The `/report` handler enforces authenticate (401) → tenant-present (400) → authorize-for-tenant (403, before Build) → build (404). The existing `authToken` becomes an all-tenants admin token; no DB.

**Tech Stack:** Go 1.26 stdlib (`crypto/sha256`, `net/http`), existing config/render packages. No new dependencies.

Spec: `docs/superpowers/specs/2026-06-06-backup-report-phase4b-design.md`.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/config/report.go` (modify) | `ReportToken` type + `Tokens` field on `ReportOutput`; interpolate + validate |
| `internal/config/report_test.go` (modify) | tokens parse/validation tests |
| `internal/report/render/authz.go` (create) | `TokenScope`, `Scope`, `Authorizer`, `NewAuthorizer`, `Required`/`Authenticate`/`Allows` |
| `internal/report/render/authz_test.go` (create) | Authorizer unit tests |
| `internal/report/render/http.go` (modify) | `NewHandler` takes `*Authorizer`; auth block 401/400/403; remove `bearerOK` |
| `internal/report/render/http_test.go` (modify) | update existing `NewHandler` calls + add per-tenant tests |
| `cmd/report/main.go` (modify) | build the authorizer from config |
| `config.report.demo.yaml`, `docs/report.md`, `CHANGELOG.md` (modify) | demo + docs |

**Conventions:** parameterized SQL only (n/a here); no inline lint/semgrep suppressions; `gofmt -w` before each commit; commit trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. `render` must NOT import `internal/config` (main maps config → `render.TokenScope`). Handler tests live in `package render` and use `storeFor(t)` (from `data_test.go`, seeds tenant `acme`) and `reporttest.NewStore`.

---

## Task 1: Config — `report.tokens`

**Files:**
- Modify: `internal/config/report.go`
- Test: `internal/config/report_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/report_test.go`:

```go
func TestLoadReportTokens(t *testing.T) {
	t.Setenv("ACME_TOKEN", "acme-secret")
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
report:
  listen: "127.0.0.1:9103"
  tokens:
    - {token: "${ACME_TOKEN}", tenants: [acme-corp]}
    - {token: "all", tenants: ["*"]}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if len(cfg.Report.Tokens) != 2 {
		t.Fatalf("tokens = %d, want 2", len(cfg.Report.Tokens))
	}
	if cfg.Report.Tokens[0].Token != "acme-secret" || cfg.Report.Tokens[0].Tenants[0] != "acme-corp" {
		t.Errorf("token0 = %+v", cfg.Report.Tokens[0])
	}
	if cfg.Report.Tokens[1].Tenants[0] != "*" {
		t.Errorf("token1 = %+v", cfg.Report.Tokens[1])
	}
}

func TestLoadReportTokenValidation(t *testing.T) {
	base := "database: {dsn: x}\nservers:\n  - {name: p, host: h, username: u, password: p}\n"
	cases := map[string]string{
		"empty token":   base + "report:\n  tokens:\n    - {token: \"\", tenants: [acme]}\n",
		"empty tenants": base + "report:\n  tokens:\n    - {token: t, tenants: []}\n",
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

Run: `go test -run 'TestLoadReportTokens|TestLoadReportTokenValidation' ./internal/config/`
Expected: build failure — `cfg.Report.Tokens undefined`.

- [ ] **Step 3: Add the type, field, interpolation, validation to `report.go`**

Add the type and field to `ReportOutput`:

```go
// ReportToken authorizes a bearer token to read the named tenants' reports ("*" = all).
type ReportToken struct {
	Token   string   `yaml:"token"`
	Tenants []string `yaml:"tenants"`
}

// ReportOutput configures Phase 3 report generation: the optional HTTP endpoint and branding.
type ReportOutput struct {
	Listen    string        `yaml:"listen"`
	AuthToken string        `yaml:"authToken"`
	BrandName string        `yaml:"brandName"`
	Tokens    []ReportToken `yaml:"tokens"`
}
```

In `LoadReport`, after the existing `report.authToken` interpolation/`brandName` default block, add:

```go
	for i := range cfg.Report.Tokens {
		tok, err := interpolate(cfg.Report.Tokens[i].Token)
		if err != nil {
			return nil, fmt.Errorf("report token %d: %w", i, err)
		}
		cfg.Report.Tokens[i].Token = tok
		if tok == "" {
			return nil, fmt.Errorf("report token %d: token is required", i)
		}
		if len(cfg.Report.Tokens[i].Tenants) == 0 {
			return nil, fmt.Errorf("report token %d: tenants required", i)
		}
	}
```

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/config/ && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/report.go internal/config/report_test.go
git commit -m "$(printf 'feat(config): report.tokens (per-tenant bearer scopes)\n\nReportToken{token, tenants} list on ReportOutput; token env-interpolated like\nauthToken; validated (non-empty token + tenants) when present. \"*\" tenant = all.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 2: `render.Authorizer`

**Files:**
- Create: `internal/report/render/authz.go`, `internal/report/render/authz_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/report/render/authz_test.go`:

```go
package render

import (
	"net/http"
	"testing"
)

func req(bearer string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/report", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func TestAuthorizerScopedAndAdmin(t *testing.T) {
	a := NewAuthorizer("admintok", []TokenScope{
		{Token: "acmetok", Tenants: []string{"acme"}},
		{Token: "alltok", Tenants: []string{"*"}},
	})
	if !a.Required() {
		t.Fatal("Required should be true")
	}
	// scoped token: allowed for its tenant, denied for another
	sc, ok := a.Authenticate(req("acmetok"))
	if !ok || !a.Allows(sc, "acme") || a.Allows(sc, "globex") {
		t.Errorf("acme scope: ok=%v acme=%v globex=%v", ok, a.Allows(sc, "acme"), a.Allows(sc, "globex"))
	}
	// "*" token: any tenant
	sc, ok = a.Authenticate(req("alltok"))
	if !ok || !a.Allows(sc, "anything") {
		t.Errorf("* token should allow any tenant")
	}
	// admin authToken: any tenant
	sc, ok = a.Authenticate(req("admintok"))
	if !ok || !a.Allows(sc, "whatever") {
		t.Errorf("admin token should allow any tenant")
	}
	// unknown / missing token: not authenticated
	if _, ok := a.Authenticate(req("nope")); ok {
		t.Error("unknown token should not authenticate")
	}
	if _, ok := a.Authenticate(req("")); ok {
		t.Error("missing bearer should not authenticate")
	}
}

func TestAuthorizerNotRequiredWhenEmpty(t *testing.T) {
	a := NewAuthorizer("", nil)
	if a.Required() {
		t.Error("Required should be false with no tokens")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestAuthorizer ./internal/report/render/`
Expected: build failure — `undefined: NewAuthorizer`.

- [ ] **Step 3: Implement `authz.go`**

```go
package render

import (
	"crypto/sha256"
	"net/http"
	"strings"
)

// TokenScope is one bearer token and the tenants it may read ("*" = all). Plain DTO so the render
// package stays free of internal/config; cmd/report maps config tokens into these.
type TokenScope struct {
	Token   string
	Tenants []string
}

// Scope is an authenticated token's authorization: all tenants, or a specific set.
type Scope struct {
	all     bool
	tenants map[string]bool
}

// Authorizer maps bearer tokens (by sha256 hash) to their Scope.
type Authorizer struct {
	byHash map[[32]byte]Scope
}

// NewAuthorizer builds the registry. A non-empty authToken is registered as an all-tenants (admin)
// token; empty token entries are ignored. Required() is false only when nothing was registered.
func NewAuthorizer(authToken string, scopes []TokenScope) *Authorizer {
	a := &Authorizer{byHash: make(map[[32]byte]Scope)}
	if authToken != "" {
		a.byHash[sha256.Sum256([]byte(authToken))] = Scope{all: true}
	}
	for _, ts := range scopes {
		if ts.Token == "" {
			continue
		}
		sc := Scope{tenants: make(map[string]bool, len(ts.Tenants))}
		for _, t := range ts.Tenants {
			if t == "*" {
				sc.all = true
			} else {
				sc.tenants[t] = true
			}
		}
		a.byHash[sha256.Sum256([]byte(ts.Token))] = sc
	}
	return a
}

// Required reports whether any token is registered (i.e. auth must be enforced).
func (a *Authorizer) Required() bool { return len(a.byHash) > 0 }

// Authenticate matches a request's Bearer token to its Scope; ok=false if the token is absent or
// unknown. Lookup is a single map access on the sha256 hash (no per-token comparison).
func (a *Authorizer) Authenticate(r *http.Request) (Scope, bool) {
	h := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(h, "Bearer ")
	if tok == h { // "Bearer " prefix absent
		return Scope{}, false
	}
	sc, ok := a.byHash[sha256.Sum256([]byte(tok))]
	return sc, ok
}

// Allows reports whether an authenticated Scope may read tenant.
func (a *Authorizer) Allows(s Scope, tenant string) bool {
	return s.all || s.tenants[tenant]
}
```

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/report/render/ && go test -run TestAuthorizer ./internal/report/render/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/render/authz.go internal/report/render/authz_test.go
git commit -m "$(printf 'feat(render): Authorizer — per-tenant bearer token registry\n\nsha256-keyed token->Scope map; authToken = all-tenants admin, \"*\" = all.\nAuthenticate(req) + Allows(scope, tenant) + Required(); config-free (TokenScope\nDTO mapped by cmd/report).\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 3: Handler — enforce per-tenant authz

**Files:**
- Modify: `internal/report/render/http.go`, `internal/report/render/http_test.go`

- [ ] **Step 1: Update + add the failing tests**

In `internal/report/render/http_test.go`, change the three `NewHandler(...)` calls to pass an `*Authorizer`:
- `TestHandlerServesReport`: `h := NewHandler(st, "Acme Co", NewAuthorizer("", nil))` (no auth).
- `TestHandlerValidation`: `h := NewHandler(st, "Acme Co", NewAuthorizer("tok", nil))` (admin token "tok"; the existing 401/400/404/200 assertions still hold because an admin token authorizes any tenant, so `?tenant=ghost` → 404 via Build, unchanged).
- `TestHandlerRejectsNonGET`: `srv := httptest.NewServer(NewHandler(st, "B", NewAuthorizer("", nil)))`.

Then append the per-tenant test:

```go
func TestHandlerPerTenantAuthz(t *testing.T) {
	st := storeFor(t) // tenant "acme" has data; "globex" does not
	h := NewHandler(st, "Acme Co", NewAuthorizer("", []TokenScope{
		{Token: "acmetok", Tenants: []string{"acme"}},
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	get := func(path, bearer string) int {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
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
	if c := get("/report?tenant=acme", "acmetok"); c != 200 {
		t.Errorf("in-scope tenant -> %d, want 200", c)
	}
	if c := get("/report?tenant=globex", "acmetok"); c != 403 {
		t.Errorf("out-of-scope tenant -> %d, want 403", c)
	}
	// authorize-before-build: a nonexistent tenant outside scope is 403, not a 404 existence oracle.
	if c := get("/report?tenant=ghost", "acmetok"); c != 403 {
		t.Errorf("out-of-scope nonexistent tenant -> %d, want 403 (no existence oracle)", c)
	}
	if c := get("/report?tenant=acme", "wrong"); c != 401 {
		t.Errorf("unknown token -> %d, want 401", c)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestHandler ./internal/report/render/`
Expected: build failure — `NewHandler(...)` signature mismatch / `too many arguments` (string vs *Authorizer).

- [ ] **Step 3: Rewrite the handler in `http.go`**

Change the import block to drop `crypto/sha256`, `crypto/subtle`, and `strings` (all now used only by the deleted `bearerOK`, which moves to `authz.go`):

```go
import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	log "github.com/sirupsen/logrus"
)
```

Change `NewHandler` to take an `*Authorizer` and drop the `requireAuth`/`wantHash` locals:

```go
// NewHandler returns the read-only report HTTP surface: GET /report?tenant=&format= and
// GET /healthz. authz enforces per-tenant access; when no tokens are configured it is a no-op
// (the endpoint is open — localhost posture).
func NewHandler(st *report.Store, brand string, authz *Authorizer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		secureHeaders(w)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var scope Scope
		if authz.Required() {
			s, ok := authz.Authenticate(r)
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			scope = s
		}
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			http.Error(w, "tenant is required", http.StatusBadRequest)
			return
		}
		if authz.Required() && !authz.Allows(scope, tenant) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ext, err := FormatExt(r.URL.Query().Get("format"))
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
		renderFn, contentType := RenderHTML, "text/html; charset=utf-8"
		if ext == "pdf" {
			renderFn, contentType = RenderPDF, "application/pdf"
		}
		var buf bytes.Buffer
		if err := renderFn(&buf, data); err != nil {
			log.WithError(err).Warn("render report failed")
			http.Error(w, "render failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		writeBytes(w, &buf)
	})
	return mux
}
```

Delete the `bearerOK` function from `http.go` (its sha256/subtle/strings logic now lives in `authz.go`). Leave `writeBytes`, `secureHeaders`, and `FormatExt` unchanged.

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/report/render/ && go test ./internal/report/render/`
Expected: PASS (existing handler tests + `TestHandlerPerTenantAuthz`).

- [ ] **Step 5: Commit**

```bash
git add internal/report/render/http.go internal/report/render/http_test.go
git commit -m "$(printf 'feat(render): enforce per-tenant authz on /report\n\nNewHandler takes *Authorizer; flow is authenticate(401) -> tenant(400) ->\nauthorize-for-tenant(403, before Build) -> build(404). Out-of-scope tokens get\n403 with no tenant-existence oracle. bearerOK removed (folded into Authorizer).\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 4: Wire the authorizer in `cmd/report`

**Files:**
- Modify: `cmd/report/main.go`

- [ ] **Step 1: Update the wiring**

In `run(...)`, replace the HTTP-handler construction
`h := render.NewHandler(store, cfg.Report.BrandName, cfg.Report.AuthToken)` with:

```go
			scopes := make([]render.TokenScope, 0, len(cfg.Report.Tokens))
			for _, t := range cfg.Report.Tokens {
				scopes = append(scopes, render.TokenScope{Token: t.Token, Tenants: t.Tenants})
			}
			authz := render.NewAuthorizer(cfg.Report.AuthToken, scopes)
			h := render.NewHandler(store, cfg.Report.BrandName, authz)
```

(Keep the surrounding `if cfg.Report.Listen != "" { ... }` block and the rest unchanged.)

- [ ] **Step 2: Build + vet**

Run: `gofmt -w cmd/report/ && go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add cmd/report/main.go
git commit -m "$(printf 'feat(report): build the /report Authorizer from config tokens\n\nMap cfg.Report.AuthToken + cfg.Report.Tokens into a render.Authorizer and pass it\nto NewHandler.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 5: Demo + docs

**Files:**
- Modify: `config.report.demo.yaml`, `docs/report.md`, `CHANGELOG.md`

- [ ] **Step 1: Add a scoped token to the demo report block**

In `config.report.demo.yaml`, under the existing `report:` block, add a `tokens` entry (read the file first to place it correctly):

```yaml
report:
  listen: "0.0.0.0:9103"
  brandName: "Acme Backup Assurance"
  tokens:
    - {token: "demo-acme-token", tenants: [acme-corp]}   # demo only — use ${ENV} + a strong token in prod
```

- [ ] **Step 2: Document `report.tokens` in `docs/report.md`**

Append to the "Assurance report" / endpoint section:

````markdown
### Per-tenant access (Phase 4b)

Scope `/report` bearer tokens to tenants. `authToken` (if set) is an all-tenants admin token;
each `tokens` entry grants its `token` access to the listed `tenants` (`"*"` = all):

```yaml
report:
  authToken: "${ADMIN_TOKEN}"          # optional, all tenants
  tokens:
    - {token: "${ACME_TOKEN}", tenants: [acme-corp]}
```

```bash
curl -H "Authorization: Bearer $ACME_TOKEN" '127.0.0.1:9103/report?tenant=acme-corp'  # 200
curl -H "Authorization: Bearer $ACME_TOKEN" '127.0.0.1:9103/report?tenant=globex'     # 403
curl '127.0.0.1:9103/report?tenant=acme-corp'                                          # 401 (token required)
```

When no `authToken` and no `tokens` are configured, the endpoint is unauthenticated (localhost posture).
````

- [ ] **Step 3: CHANGELOG entry**

Under `## [Unreleased]` → `### Added` in `CHANGELOG.md` (read to find the spot; match the Phase 4a entry style):

```markdown
- `cmd/report` Phase 4b: per-tenant `/report` access control — `report.tokens` scope each bearer
  token to specific tenants (`authToken` = all-tenants admin); an out-of-scope token gets 403
  before any data access. No new dependencies.
```

- [ ] **Step 4: Validate + commit**

Run: `ruby -ryaml -e 'YAML.safe_load(File.read("config.report.demo.yaml"))' && docker compose config -q` (expect no error).

```bash
git add config.report.demo.yaml docs/report.md CHANGELOG.md
git commit -m "$(printf 'docs(report): Phase 4b per-tenant token docs + demo token\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 6: Gate + security + push

- [ ] **Step 1: Full CI gate**

Run: `make ci`
Expected: gofmt/vet clean, golangci-lint `0 issues`, `go test -race ./...` pass, govulncheck `No vulnerabilities found`, build OK.

- [ ] **Step 2: Semgrep**

Scan the changed Go files (`internal/report/render/authz.go`, `http.go`, `internal/config/report.go`,
`cmd/report/main.go`) via the semgrep MCP tool or `semgrep --config auto`. Expect 0 findings; no inline suppressions.

- [ ] **Step 3: Security review**

Phase 4b changes the auth model. Run `/security-review` on the branch (focus: token hashing/comparison,
the 401/403 ordering + no tenant-existence oracle, no token logging, the no-auth posture being explicit).
Address high-confidence findings.

- [ ] **Step 4: Push**

```bash
git push -u origin feat/backup-report-phase4b
```

---

## Self-review notes (spec coverage)

- Spec §1 config tokens → Task 1. §2 Authorizer (config-free, sha256 map, Required/Authenticate/Allows) → Task 2. §3 handler 401→400→403→404 ordering + authorize-before-build → Task 3. §4 wiring → Task 4. Testing → tests in Tasks 1-3 + Task 6 gate. Demo/docs → Task 5.
- Type/name consistency: `config.ReportToken{Token,Tenants}`; `render.TokenScope{Token,Tenants}`, `render.Scope`, `render.Authorizer`, `render.NewAuthorizer(authToken, []TokenScope)`, `Required()/Authenticate(r)(Scope,bool)/Allows(Scope,tenant)`; `NewHandler(*report.Store, string, *Authorizer)`.
- Existing `http_test.go` calls updated (string → `NewAuthorizer(...)`); the admin-token case preserves the prior 401/400/404/200 expectations (admin authorizes any tenant, so the `ghost` case is still a Build-404, not a 403).
- `bearerOK` removed from `http.go`; `crypto/sha256`/`crypto/subtle`/`strings` imports dropped there (moved to `authz.go`).
