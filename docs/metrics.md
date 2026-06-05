# Metrics

Every metric carries a `server` label identifying the PPDM server, so one exporter
serves many servers. Collection follows the snapshot model: a background loop polls
each server on `collection.interval` and both `/metrics` and OTLP read the latest
snapshot.

## Health & liveness

| Metric | Labels | Type | Meaning |
|---|---|---|---|
| `ppdm_up` | `server` | gauge | `1` if the server was reachable and authenticated this cycle, else `0`. |
| `ppdm_collector_up` | `server`, `collector` | gauge | `1` if that domain collector (`activities`/`assets`/`capacity`/`health`) succeeded, else `0`. |

## Activities (jobs) — `/api/v2/activities`

Aggregated over the `collection.lookback` window (default 24h).

| Metric | Labels | Type | Meaning |
|---|---|---|---|
| `ppdm_activity_count` | `server`, `category`, `result_status` | gauge | Number of activities by category (`PROTECT`/`RESTORE`/…) and outcome (`SUCCESS`/`FAILED`/running state). |
| `ppdm_activity_bytes_total` | `server`, `category`, `result_status` (empty) | gauge | Sum of `result.bytesTransferred` per category over the window. **A windowed sum recomputed each cycle — aggregate with `sum`/`max`, never `rate()`.** |

Example alert — backups failing:

```promql
sum by (server) (ppdm_activity_count{category="PROTECT", result_status="FAILED"}) > 0
```

### Per-job detail (opt-in)

Set `collection.perJobActivities: true` to additionally emit one series set per job — the
data behind the *Backup Report* dashboard's per-job table. **Higher cardinality** (≈ one
info series per job in the window); off by default.

| Metric | Labels | Type | Meaning |
|---|---|---|---|
| `ppdm_activity_info` | `server`, `activity_id`, `name`, `category`, `subcategory`, `result_status`, `asset`, `policy` | gauge (`1`) | One row per job; descriptive labels for a report table. |
| `ppdm_activity_job_bytes` | `server`, `activity_id` | gauge | `result.bytesTransferred` for that job. |
| `ppdm_activity_job_duration_seconds` | `server`, `activity_id` | gauge | `completedAt − startedAt`; omitted for jobs still running. |

A Grafana table joins these by `activity_id`. The `asset`/`policy`/`startedAt`/`completedAt`
fields are **provisional** (ADR-0009).

## Asset protection — `/api/v2/assets`

| Metric | Labels | Type | Meaning |
|---|---|---|---|
| `ppdm_asset_count` | `server`, `type`, `protection_status` | gauge | Asset count by type and `protection_status` (`PROTECTED`/`UNPROTECTED`/`LAPSED`). |
| `ppdm_asset_unprotected` | `server`, `type` (empty), `protection_status` (empty) | gauge | Rollup count of non-`PROTECTED` assets. |
| `ppdm_asset_last_copy_age_seconds` | `server`, `asset`, `type`, `protection_status` | gauge | Seconds since an asset's last available copy. **Bounded:** emitted only for at-risk assets — non-`PROTECTED`, or `PROTECTED` but older than `collection.assetAgeThreshold`. Healthy on-time assets emit no series. |

Example alert — an asset overdue beyond 26h:

```promql
ppdm_asset_last_copy_age_seconds > 26*3600
```

## Capacity — `/api/v2/datadomain-mtrees`, `/api/v2/storage-systems`

> ⚠️ Field shapes are **provisional** (unconfirmed by any source; see ADR-0009).
> Authoritative Data Domain capacity is also available via `ppdd_exporter`.

| Metric | Labels | Type | Meaning |
|---|---|---|---|
| `ppdm_storage_unit_physical_capacity_bytes` | `server`, `storage_unit` | gauge | MTree physical capacity. |
| `ppdm_storage_unit_physical_used_bytes` | `server`, `storage_unit` | gauge | MTree physical used. |
| `ppdm_storage_unit_logical_used_bytes` | `server`, `storage_unit` | gauge | MTree logical (pre-dedup) used. |
| `ppdm_storage_system_total_bytes` | `server`, `storage_system`, `type` | gauge | Storage-system total capacity. |
| `ppdm_storage_system_used_bytes` | `server`, `storage_system`, `type` | gauge | Storage-system used. |

## Health & alerts — `/api/v3/health-entities`, `/api/v2/alerts`

| Metric | Labels | Type | Meaning |
|---|---|---|---|
| `ppdm_health_entity_status` | `server`, `entity`, `component` | gauge | `1` when the entity status is `OK`, else `0`. |
| `ppdm_alert_count` | `server`, `severity`, `ack_state` | gauge | Open alert count by `severity` (`CRITICAL`/`WARNING`/`INFORMATIONAL`) and `ack_state` (`acknowledgement.acknowledgeState`). |

## Conventions

- **Per-second / rate values are gauges** and would be aggregated with `sum`/`avg`,
  never `rate()`. (v1 emits no per-second metric — the only candidate was dropped as
  semantically unsound.)
- **Label-key consistency invariant:** each metric name carries one ordered label-key
  set across all its series; rollups use empty values for inapplicable keys. Enforced
  by `internal/ppdm/labels_test.go` (ADR-0006).
