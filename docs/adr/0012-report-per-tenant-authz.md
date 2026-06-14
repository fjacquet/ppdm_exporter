# Per-tenant authorization for the `/report` endpoint

## Status
Accepted.

## Context
The reporter (ADR-0011) serves rendered backup history over `/report`. The captured data is
multi-tenant — rows carry a `tenant` column — and different consumers must see only their own
tenant's history, while operators need an all-tenants view. The exporter's `/metrics` has no
such need (it is internal and single-scope), so this is a reporter-only concern. A token must
never be logged or compared byte-by-byte in a way that leaks timing or appears in traces.

## Decision
Introduce an `Authorizer` (`internal/report/render`) backed by a bearer-token registry.

- Tokens are stored and looked up **by `sha256` hash**, never in cleartext; authentication is
  a single map access on the request token's hash (no per-token comparison loop).
- Each token maps to a `Scope`: **all tenants** (admin) or an explicit **set of tenant names**
  (`"*"` in a scope promotes it to all-tenants). Config supplies these via `report.tokens`
  (per-tenant bearer scopes); a non-empty top-level `authToken` registers an all-tenants admin
  token.
- `Required()` is false only when **nothing** was registered — i.e. auth is enforced as soon
  as any token exists, and an unconfigured reporter is open (operator opt-in).
- The render package stays free of `internal/config`: `cmd/report` maps config tokens into
  plain `TokenScope` DTOs. Duplicate-tenant override entries are rejected at config load.

## Consequences
`/report` enforces least-privilege per tenant with a constant-time hash lookup and no token
material in memory beyond the hash. The "no tokens registered = open" default keeps local/demo
runs frictionless but means production deployments **must** configure at least one token to
close the endpoint — this is the operator's responsibility. Adding scopes (read vs. render,
rate limits) is a future extension of `Scope`, not a rewrite.
