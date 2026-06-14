# Validate PPDM structs against 20.1.0 OpenAPI specs — design

Date: 2026-06-13

## Problem

Our deserialization structs were modeled from the PPDM **19.22.0** reference and
cross-checked against the Apache-2.0 `dell/powerprotect-data-manager` module. Every
unverified field carries a `// provisional` tag (ADR-0009). We now have three
authoritative **20.1.0** OpenAPI 3 specs:

- `9765-20.1.0.json` — v2 API (377 paths)
- `9628-20.1.0.json` — v3 API (85 paths)
- `9627-20.1.0.json` — dpilm v1 (license/registrations) — **out of scope**, not consumed

This is the authoritative surface to resolve every `provisional` field into confirmed,
corrected, or knowingly-divergent — and to catch silent bugs. An early probe already
found one: our `Activity` struct (and the report `Job` DTO) use `startedAt`/`completedAt`,
but the 20.1.0 `Activity` schema has neither — it exposes `createTime`/`endTime`.

## Goal

A committed validation report plus TDD-applied corrections, covering all structs we
deserialize from PPDM (exporter collectors + report capture DTOs).

## Scope — struct → spec → component

| Go struct | Endpoint | Spec | Component |
|---|---|---|---|
| `ppdm.activity` (internal/ppdm/activities.go) | `/api/v2/activities` | 9765 | `Activity` |
| `ppdm.asset` (internal/ppdm/assets.go) | `/api/v2/assets` | 9765 | `Asset` content item |
| `ppdm.mtree` (internal/ppdm/capacity.go) | `/api/v2/datadomain-mtrees` | 9765 | content item |
| `ppdm.storageSystem` (internal/ppdm/capacity.go) | `/api/v2/storage-systems` | 9765 | content item |
| `ppdm.healthEntity` (internal/ppdm/health.go) | `/api/v3/health-entities` | 9628 | content item |
| `ppdm.alert` (internal/ppdm/health.go) | `/api/v2/alerts` | 9765 | `Alert` |
| `report.Job` (internal/report/models.go) | `/api/v2/activities` | 9765 | `Activity` |
| `report.Copy` (internal/report/models.go) | `/api/v2/copies` | 9765 | `Copy` |
| `report.Asset` (internal/report/models.go) | `/api/v2/assets` | 9765 | `Asset` |
| `report.Policy` (internal/report/models.go) | `/api/v3/protection-policies` | 9628 | `ProtectionPolicy` |

Schemas are reached by resolving `$ref` chains
(`paths[...].get.responses["200"].content["application/json"].schema` →
`#/components/schemas/...` → `content.items.$ref`) with `jq`. No field is judged from
memory.

## Phase 1 — Validation report (artifact: docs/adr/0010)

A permanent finding doc, `docs/adr/0010-20.1.0-api-validation.md`, indexed in
`docs/adr/index.md`. It extends ADR-0009's "grep provisional" surface with concrete
20.1.0 evidence.

Per struct, a table:

| Go field | json tag | our type | 20.1.0 type | verdict | provisional disposition |
|---|---|---|---|---|---|

Verdicts:
- ✅ **confirmed** — name + type match → retire the `provisional` tag
- ❌ **wrong** — name or type mismatch → must fix
- ⚠️ **spec-lacks** — we read a field 20.1.0 doesn't define → keep + annotate why, or drop
- ➕ **spec-has** — a useful field we ignore → noted for a *later* decision, not this scope

A top summary ranks actionable fixes by impact: a wrong field name = silent data loss
(field stays zero/empty, no error); a type mismatch = parse failure. Each report DTO row
also flags whether a rename ripples into `internal/report/migrations.sql` (a renamed Go
field feeding a Postgres column).

## Phase 2 — TDD fixes, one struct per cycle

Order: exporter collectors first, then the report DTOs (so the shared `Activity` shape is
settled once in the collector before `report.Job`). For each struct with ❌/⚠️ findings:

1. Update the struct's single fixture to the real 20.1.0 shape (one fixture per shape).
2. Run the test → **red** (proves the old field was wrong / unread).
3. Correct the struct field name/type; retire `provisional` on confirmed fields; keep and
   annotate any field 20.1.0 genuinely lacks (with the divergence reason).
4. Run the test → **green**. Run `make sure` between structs.

Where a corrected field feeds a Prometheus label or metric value, `labels_test.go` remains
the gate. Where it feeds a Postgres column, update `migrations.sql` in the same cycle and
keep the upsert idempotent.

## Constraints honored

- **One struct + one fixture per shape** — fixing a wrong field touches one file (ADR-0009).
- **Label-key invariant** (ADR-0006) — we correct field *sources*, not label-key sets;
  `labels_test.go` still gates any value that flows to a label.
- **No new endpoints** — correctness-only. Richer fields the spec reveals land as ➕ notes,
  deferred, not scope creep.
- **Trace never logs tokens** (unchanged) — no client/auth changes in this effort.
- **No inline semgrep/lint suppressions** — restructure if a fix tempts one.

## Testing

TDD throughout. Collectors driven by `ppdmclient.Mock` fixtures; report DTOs by their
existing unit tests. Assert via both the Prometheus registry and the OTLP `ManualReader`
where a corrected field reaches a metric. `make sure` is the per-struct gate; `make ci`
before finishing.

## Out of scope

- 9627 dpilm v1 (license-details, registrations) — not consumed.
- Adding any ➕ "spec-has" field as a new metric/column — recorded for a future effort.
- Client, auth, pagination, trace, config — untouched.

## Deliverables

1. `docs/adr/0010-20.1.0-api-validation.md` (+ index entry) — the validation report.
2. Corrected structs + fixtures + tests for every ❌/⚠️ finding.
3. Retired `provisional` tags on all ✅ fields; `grep -rn provisional internal/` reflects
   only genuinely-still-unverified shapes afterward.
