# PPDM Exporter — Design Spec

**Date:** 2026-06-05
**Status:** Approved (brainstorming review of the initial plan)
**Plan:** `docs/superpowers/plans/2026-06-05-ppdm-exporter.md` (to be reconciled with this spec)

## Purpose

A Go Prometheus + OTLP exporter for Dell PowerProtect Data Manager (PPDM). One process polls many PPDM servers on an interval, publishes an immutable snapshot, serves it at `/metrics`, and pushes the same snapshot over OTLP. It is the empty-skeleton member of the family standardized by the `exporter-standards` skill, conformed to the hand-rolled siblings `ppdd`/`pflex`.

## Scope (v1)

Four PPDM resource families: **activities** (jobs), **asset protection**, **capacity** (DataDomain MTrees + storage systems), **health & alerts**. Dual export (Prometheus + OTLP) from day one. Hand-rolled `resty/v2` client.

Out of scope for v1: per-job duration histograms, replication-lag metrics, multi-tenant breakdowns, the `refresh_token` flow (re-login suffices given the 30-min access token).

## Foundational facts (validated against the PPDM 19.22.0 REST API PDF)

- **Server:** PPDM 19.22.0, port **8443**, `/api/v2` + `/api/v3`.
- **Auth:** `POST /api/v2/login {username,password}` → `{access_token, token_type:"Bearer", expires_in:1800, refresh_token, scope}`. Subsequent calls send `Authorization: Bearer <access_token>`.
- **Pagination:** list endpoints return `{"content":[...],"page":{"number","size","totalPages","totalElements"}}`, requested with `?page=<n>&pageSize=<s>` (0-indexed).
- **Activities:** each item has `category` (`PROTECT`/`RESTORE`/`DISCOVERY`/`REPLICATE`/...), `state`, `result.status` (`SUCCESS`/`FAILED`/`OK`/...), and **`result.bytesTransferred`** (cumulative, summable — confirmed on `PROTECT` activities). `statistics.{numberOfAssets,runDurationMillis}` exist but are unused in v1. `statistics.bytesTransferredPerSecond` exists but is **not** used (summing instantaneous rates is meaningless).
- **Assets:** `type`, `protectionStatus` (`PROTECTED`/`UNPROTECTED`/`LAPSED`), `lastAvailableCopyTime` (RFC3339 or null).
- **Capacity (provisional, highest correction risk):** MTree + storage-system capacity field names modeled, not confirmed. The PDF itself is ambiguous here — the only capacity examples it surfaces are VMware datastore/VM-disk blocks, not a clean per-MTree/per-storage-system capacity model — so these fields are unconfirmed by *every* source. Note: authoritative Data Domain capacity is already available via the sibling `ppdd_exporter` (talks to DD directly); PPDM's own strength is jobs/assets/protection.
- **Health/alerts:** `alerts.severity` is PDF-confirmed enum `CRITICAL`/`WARNING`/`INFORMATIONAL`. `alerts.acknowledgement` is an **opaque `{}` object in the PDF**; the Apache-2.0 reference module (`Example-03.ps1`) fills it in as `acknowledgement.acknowledgeState` (a string enum) — so alerts use `ack_state`, **not** a boolean `acknowledged`. `health-entities` response carries `componentType`; its `status`/`healthState` enum is unconfirmed (provisional).

## Architecture

The family snapshot model. One background **collection loop** logs into every configured server, runs a set of modular per-domain `ResourceCollector`s in parallel (`errgroup`), builds an immutable `Snapshot`, and pointer-swaps it into a `SnapshotStore` (RWMutex). Both export paths read the latest snapshot:

```
loop → login → [activities | assets | capacity | health] → Snapshot → SnapshotStore.Swap()
                                                              ├── PromCollector (/metrics, unchecked)
                                                              └── OTLPExporter (observable gauges, periodic reader)
```

- **Serve HTTP before the first collection cycle** (a slow first login must not stall `/metrics`).
- **Graceful degradation:** a per-collector failure emits `ppdm_collector_up{collector}=0` and marks the server `ppdm_up=0`, but never fails the cycle or other servers.
- **Identity label `server`** on every metric — one process, many PPDM servers.
- **Config hot reload** via SIGHUP + fsnotify (rebuild-and-swap).

### Components (each one struct + one fixture, isolated)

| Package / file | Responsibility |
|---|---|
| `internal/ppdmclient/{client,server,auth,paginate,mock}.go` | Bearer-auth resty client; generic `GetAll[T]` paginator; in-memory mock |
| `internal/config/{config,watcher}.go` | YAML config, `${ENV}`/`passwordFile`, defaults, hot reload |
| `internal/ppdm/{sample,snapshot,resource,collector}.go` | Metric model, snapshot store (+ `MetricNames`/`SamplesByName`), collector loop |
| `internal/ppdm/{activities,assets,capacity,health}.go` | The four domain collectors |
| `internal/ppdm/{prometheus,otlp}.go` | Dual export over the snapshot |
| `internal/telemetry/manager.go` | Optional OTLP tracer provider |

## Metric catalog (final)

| Metric | Labels | Source | Notes |
|---|---|---|---|
| `ppdm_up` | `server` | loop | 1 = reachable + authenticated |
| `ppdm_collector_up` | `server, collector` | loop | per-domain health |
| `ppdm_activity_count` | `server, category, result_status` | `/api/v2/activities` | windowed outcome counts |
| `ppdm_activity_bytes_total` | `server, category, result_status=""` | `result.bytesTransferred` | windowed sum, emitted as gauge |
| `ppdm_asset_count` | `server, type, protection_status` | `/api/v2/assets` | rollup over all assets |
| `ppdm_asset_unprotected` | `server, type="", protection_status=""` | `/api/v2/assets` | rollup of non-PROTECTED |
| `ppdm_asset_last_copy_age_seconds` | `server, asset, type, protection_status` | `/api/v2/assets` | **at-risk assets only** (see below) |
| `ppdm_storage_unit_physical_capacity_bytes` | `server, storage_unit` | `/api/v2/datadomain-mtrees` | provisional fields |
| `ppdm_storage_unit_physical_used_bytes` | `server, storage_unit` | `/api/v2/datadomain-mtrees` | provisional fields |
| `ppdm_storage_unit_logical_used_bytes` | `server, storage_unit` | `/api/v2/datadomain-mtrees` | provisional fields |
| `ppdm_storage_system_total_bytes` | `server, storage_system, type` | `/api/v2/storage-systems` | provisional fields |
| `ppdm_storage_system_used_bytes` | `server, storage_system, type` | `/api/v2/storage-systems` | provisional fields |
| `ppdm_health_entity_status` | `server, entity, component` | `/api/v3/health-entities` | 1 = OK, else 0 |
| `ppdm_alert_count` | `server, severity, ack_state` | `/api/v2/alerts` | rollup; `ack_state` from `acknowledgement.acknowledgeState` |

**Dropped vs. the initial plan:** `ppdm_activity_bytes_per_second` (broken summed rate), `ppdm_activity_assets` (unused).

### Cardinality strategy (Q1 — hybrid)

Rollups are emitted for everything (cheap, fleet-independent). Per-asset detail is bounded to assets worth acting on: `ppdm_asset_last_copy_age_seconds` is emitted for an asset **only when** `protection_status != "PROTECTED"` **or** `now - lastAvailableCopyTime > collection.assetAgeThreshold`. A healthy, on-time fleet emits no per-asset series; emitted cardinality tracks the problem count, not the asset count. (`lastAvailableCopyTime == null` → emitted with a sentinel/omitted value but the series is present to flag the asset.)

### Naming & units

- `ppdm_` prefix; `server` identity label; SI bytes/seconds.
- Per-second values would be gauges aggregated with `sum`/`avg`, never `rate()` — but v1 emits no per-second metric (the one candidate was dropped).
- **Label-key invariant (load-bearing):** each metric name carries one ordered label-key set across all its series; rollups use empty values for inapplicable keys. `ppdm_activity_*` → `{category, result_status}`; `ppdm_asset_count`/`ppdm_asset_unprotected` → `{type, protection_status}`; `ppdm_asset_last_copy_age_seconds` is a distinct name with its own fixed set `{asset, type, protection_status}`. Enforced by `labels_test.go`.

## Config additions

```yaml
collection:
  interval: "5m"
  timeout: "60s"
  lookback: "24h"           # activities query window (createdAt ge)
  assetAgeThreshold: "24h"  # per-asset last_copy_age emit cutoff
otel:
  enabled: false
  endpoint: "localhost:4317"
  insecure: true
  interval: "30s"
```

## Error handling

- Client retries 5xx only; **never retries 4xx** (auth/permission must not loop).
- Re-login on token expiry (within 60s) and once on any 401.
- Per-collector errors degrade to `*_up=0`; per-server login failure → `ppdm_up=0`, other servers unaffected.
- Unbuildable Prometheus metrics (inconsistent label sets) are skipped, not panicked.

## Testing

- TDD throughout. Mock PPDM via the in-memory `Mock` client (collectors) and `httptest` TLS server (client/auth/pagination).
- Every collector asserted against a `// provisional` fixture.
- Dual-path parity: assert via the Prometheus registry gather **and** an OTLP `ManualReader` over the same snapshot.
- `labels_test.go` enforces the label-key invariant across all collectors.
- Semgrep clean (no inline suppressions — restructure, e.g. the `writeBytes` test helper).

## Validation posture (Q4 — fixtures-only)

No live PPDM during development. The provisional-isolation design is the safety net: each API shape is one struct + one fixture, every unverified field is tagged `// provisional`, and **ADR-0009 is the single canonical live-validation checklist**. `grep -rn "provisional"` enumerates everything to confirm. Correcting a field on a live server = edit one struct + one fixture + rerun one test.

### Validation sources

Three sources, in order of authority for shape confirmation:

1. **The PPDM 19.22.0 REST API PDF** (`documentPPDM.pdf`, 4245 pp) — the authoritative reference; the same content Dell renders at developer.dell.com (the OpenAPI/Postman is **not** directly downloadable, so the PDF is the best machine-greppable form). Extracted to `/tmp/ppdm.txt`.
2. **`dell/powerprotect-data-manager`** (GitHub, **Apache-2.0**, maintained Feb 2026) — a PowerShell module (`PowerShell7/dell.ppdm.psm1`) + Python scripts. *Cross-reference only* — confirms which endpoints/fields production code actually uses, and the OData filter syntax (`"name eq 'value'"`, `createdAt ge "..."`). It is more specific than the PDF for the alert `acknowledgement.acknowledgeState` field. Attribute it where field definitions are copied.
3. **A live PPDM server** — the only source that confirms the genuinely-unconfirmed shapes (capacity, health-entities status enum, a few asset/activity fields). Deferred to ADR-0009.

ADR-0009 splits its checklist into **validated-by-reference** (auth, pagination, activities `state`/`result.status`, alerts pattern, assets `type`) and **still-unconfirmed** (MTree + storage-system capacity field names, `health-entities` status enum, `protectionStatus`/`lastAvailableCopyTime`, `result.bytesTransferred`, `acknowledgeState` enum values). Only the second bucket needs a server.

## Decisions (this review)

1. **Asset cardinality = hybrid (C):** rollups + bounded per-asset `last_copy_age_seconds` for at-risk assets, gated by `assetAgeThreshold`.
2. **Activity metrics = counts + total bytes (B, verified):** `result.bytesTransferred` exists and is summable; drop the per-second rate.
3. **OTLP in v1 (B):** full dual export now, matching the family standard.
4. **Fixtures-only build (C):** no live server; lean on isolation + ADR-0009.
5. **Cross-reference the Apache-2.0 official repo:** mined `dell/powerprotect-data-manager` to de-risk shapes — confirmed auth/pagination/activities/assets patterns, corrected the alert ack field to `acknowledgement.acknowledgeState` (label `ack_state`), and confirmed capacity/health-entities are unconfirmed by every available source.

Client choice (hand-rolled `resty/v2`, ADR-0003), bearer auth (ADR-0004), snapshot model, serve-before-collect, hot reload, and the full Phase 6 universal layer are unchanged from the plan.
