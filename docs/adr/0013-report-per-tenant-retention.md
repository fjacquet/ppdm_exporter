# Per-tenant retention for captured history

## Status
Accepted.

## Context
The reporter persists backup history to Postgres (ADR-0011) and prunes old rows. A single
global retention window is too blunt for a multi-tenant store: tenants have different
compliance obligations, and some need longer history than the default. Retention must be
configurable per tenant without forcing every tenant onto the longest window.

## Decision
Add a `report.retention` config block resolved per tenant.

- `Retention.DefaultDays` applies to any tenant without an override; when unset it falls back
  to `capture.retentionDays` (itself defaulting to 400). `DefaultDays` must be `> 0`.
- `Retention.Overrides` is a list of per-tenant `{tenant, days}` entries; `DaysFor(tenant)`
  returns the override if present, else `DefaultDays`. **Duplicate-tenant overrides are
  rejected at config load** (a tenant must have exactly one window).
- The capturer receives `cfg.Retention` and prunes **per tenant** using the resolved window;
  it also **backfills** the per-tenant retention setting so existing rows are governed by the
  current policy.

## Consequences
Each tenant's history is pruned to its own window; a long-retention tenant does not bloat the
store for everyone, and a short-retention tenant is not over-kept. The default-fallback chain
(`override → retention.defaultDays → capture.retentionDays → 400`) keeps minimal configs
working. Because prune is destructive and per-tenant, an override mistake silently shortens
history for that tenant — validation rejects duplicate tenants but cannot catch a wrong day
count. This is retention management for assurance, consistent with the non-forensic scope of
ADR-0011 (no WORM / legal hold).
