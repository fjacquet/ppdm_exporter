# Backup Assurance Reporter — Phase 4b: per-tenant /report access control

**Date:** 2026-06-06
**Status:** Approved (brainstorming)
**Builds on:** Phase 3 (`docs/superpowers/specs/2026-06-06-backup-report-phase3-design.md`) — the read-only
`GET /report` endpoint and its single global bearer token. Phase 4a/4 decomposition: 4a (delivery,
shipped), 4b (this), 4c (retention management).
**Branch:** `feat/backup-report-phase4b` (off `main`).

## Goal

Today the `/report` endpoint's single global `authToken` lets any holder read **any** tenant's report
via `?tenant=X`. Phase 4b adds **per-tenant authorization**: a request's bearer token must be scoped to
the requested tenant, so tenant A cannot read tenant B's report. Config-driven, no DB.

## Decisions (this brainstorm)

1. **Config tokens with tenant scopes** — a `report.tokens` list maps each bearer token to the
   tenant(s) it may read (or `*` for all). The existing `report.authToken` stays as an all-tenants
   (admin) token for back-compat.
2. **Enforce: authenticate → authorize-for-`?tenant` → build** — authorization happens before
   `render.Build`, so an unauthorized token gets 403 and cannot probe tenant existence (no 404 oracle).
3. **Posture unchanged** — if neither `authToken` nor `tokens` is configured, the endpoint is
   unauthenticated (localhost-only posture, documented), exactly as today. Any token configured ⇒ auth
   enforced.
4. **Hashed, O(1) matching** — tokens are looked up by `sha256` hash in a map (no per-token timing
   scan); no plaintext token comparison.

## Architecture

```text
cmd/report/main.go: render.NewAuthorizer(cfg.Report.AuthToken, cfg.Report.Tokens) -> *Authorizer
                      └─ render.NewHandler(store, brand, authz)

GET /report?tenant=X  (handler):
  secureHeaders
  method != GET                            -> 405
  authz.Required() && !authenticated       -> 401  (WWW-Authenticate: Bearer)
  tenant == ""                             -> 400  (can't authorize an empty tenant)
  authenticated && !authz.Allows(scope, X) -> 403  (before Build — no existence oracle)
  Build(X) ErrNoData                       -> 404
  render                                   -> 200
```

### 1. Config additions (`internal/config/report.go`)

```yaml
report:
  authToken: "${ADMIN_TOKEN}"        # optional; all-tenants admin token (unchanged)
  tokens:
    - {token: "${ACME_TOKEN}", tenants: [acme-corp]}
    - {token: "${OPS_TOKEN}",  tenants: [acme-corp, globex]}
    - {token: "${ALL_TOKEN}",  tenants: ["*"]}
```

New type `ReportToken{Token string \`yaml:"token"\`; Tenants []string \`yaml:"tenants"\`}`; field
`Tokens []ReportToken \`yaml:"tokens"\`` on `ReportOutput`. `LoadReport` interpolates each
`token` (like `authToken`), and validates: a non-empty `token` (after interpolation) and a non-empty
`tenants` for every entry. `"*"` in `tenants` means all tenants. No validation is triggered when
`tokens` is empty (posture unchanged).

### 2. `Authorizer` (in `internal/report/render`, config-free)

Keeps `render` decoupled from `config` (main maps config → render types, mirroring the established
pattern). A render-local input type avoids importing `config`:

```go
// TokenScope is one bearer token and the tenants it may read ("*" = all). Plain DTO so the
// render package stays free of internal/config.
type TokenScope struct { Token string; Tenants []string }

type Authorizer struct { /* required bool; byHash map[[32]byte]scope */ }

// NewAuthorizer builds the registry. authToken (when non-empty) is registered as an all-tenants
// token. Required() reports whether any token was configured.
func NewAuthorizer(authToken string, scopes []TokenScope) *Authorizer
func (a *Authorizer) Required() bool
// Authenticate returns the matched scope for a request's bearer, ok=false if the token is unknown.
func (a *Authorizer) Authenticate(r *http.Request) (scope, bool)
// Allows reports whether a matched scope may read tenant (all-scope or tenant in set).
```

Internals: a `map[[32]byte]scope` keyed by `sha256(token)`; each scope is either `all=true` or a
`map[string]bool` of tenants. Lookup is a single map access on the 32-byte hash — constant-ish, no
token-by-token comparison. The bearer is read with the existing `Bearer ` prefix handling.

### 3. Handler changes (`internal/report/render/http.go`)

`NewHandler(st *report.Store, brand string, authz *Authorizer) http.Handler` (was `authToken string`).
The `/report` handler:

- `if authz.Required()`: `scope, ok := authz.Authenticate(r)`; `!ok` → 401 (`WWW-Authenticate: Bearer`).
- After authentication, read `tenant`; if `tenant == ""` → 400. Then `if !authz.Allows(scope, tenant)`
  → **403** (`forbidden`). This ordering means tenant is read for the authz check, but no DB/Build runs
  for an unauthorized tenant — so 403 is returned before any existence check (no 404 oracle for tenants
  outside the token's scope). For the no-auth posture (`!Required()`), behavior is exactly as today.
- Remaining flow (format 400, Build 404, render 200, headers, 405) unchanged.

> Note the ordering nuance: missing-tenant (400) is checked before the authz tenant check (you can't
> authorize an empty tenant). An authenticated request with `tenant=""` → 400; with a tenant outside
> scope → 403; with an in-scope tenant that has no data → 404.

### 4. Wiring (`cmd/report/main.go`)

Replace `render.NewHandler(store, cfg.Report.BrandName, cfg.Report.AuthToken)` with:

```go
scopes := make([]render.TokenScope, 0, len(cfg.Report.Tokens))
for _, t := range cfg.Report.Tokens {
    scopes = append(scopes, render.TokenScope{Token: t.Token, Tenants: t.Tenants})
}
authz := render.NewAuthorizer(cfg.Report.AuthToken, scopes)
h := render.NewHandler(store, cfg.Report.BrandName, authz)
```

The scheduler and `report render` CLI are unaffected (authorization is HTTP-only; the CLI is local and
the scheduler delivers to configured recipients).

## Error handling

- Unknown/missing bearer when auth required → 401 with `WWW-Authenticate: Bearer`.
- Valid token, tenant outside scope → 403 (generic "forbidden" body; the real reason is not leaked).
- No-auth posture (no tokens configured) → endpoint open as today; a `secureHeaders`-only path.
- Token hashing uses `sha256`; matching is a map lookup (no plaintext compare, no length leak).

## Testing (TDD)

- **Authorizer** (unit): scoped token allows its tenant, denies another; `*` scope allows any; the
  admin `authToken` allows any; unknown token → not authenticated; `Required()` false when empty.
- **Handler** (httptest): scoped token → its tenant 200, other tenant **403**; `*`/admin token → any
  tenant 200; unknown token → 401 (+ `WWW-Authenticate`); no-auth config → 200 without a token; missing
  tenant → 400; **authorize-before-build**: token scoped to `acme` requesting a nonexistent tenant →
  **403, not 404**; non-GET → 405; `nosniff` header present.
- **Config**: `tokens` parse + `${ENV}` interpolation + validation (empty token, empty tenants).
- `make ci` parity (gofmt/vet/golangci-lint/test-race/govulncheck); **semgrep clean, no inline
  suppressions**.

## Demo / verification

- `config.report.demo.yaml` gains a scoped token for `acme-corp` (env-interpolated, with a documented
  demo value). `docs/report.md` documents `report.tokens` and shows the 200/403/401 behavior with
  `curl -H "Authorization: Bearer …"`. The demo endpoint stays reachable; without a token it still
  works only if no tokens are configured — for the demo we add a token and show that
  `?tenant=acme-corp` succeeds with the acme token and a different tenant returns 403.
- End-to-end: `curl -H "Authorization: Bearer $ACME" '.../report?tenant=acme-corp'` → 200;
  `...?tenant=other` → 403; missing/bad token → 401.

## Out of scope (4b)

DB-backed/revocable API keys, rate limiting, per-token request audit logging, scopes beyond tenant
(format/action/time-range), and per-tenant timezones/retention (4c). The exporter binary/image are
untouched; `/report` remains read-only and GET-only.
