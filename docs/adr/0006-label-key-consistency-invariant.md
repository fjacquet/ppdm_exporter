# Label-key consistency invariant

## Status
Accepted.

## Context
Prometheus requires every series of a metric name to share the same label-key set; mixing
key sets for one name causes scrape errors or dropped series. The exporter builds samples
dynamically, and some metrics are emitted by both a rollup path and a per-object path
(e.g. `ppdm_asset_*`). Family precedent: `ppdd` 0006, `pstore` parity test.

## Decision
Each metric name carries **one ordered label-key set across all its series**. Rollups use
**empty values for inapplicable keys** (e.g. `ppdm_asset_unprotected{type="",protection_status=""}`,
`ppdm_activity_bytes_total{...,result_status=""}`). Where a per-object metric needs extra
keys, it gets its **own metric name** with its own fixed key set
(`ppdm_asset_last_copy_age_seconds{asset,type,protection_status}`).
`internal/ppdm/labels_test.go` runs every collector against its fixture and fails on any
metric name whose key set varies.

## Consequences
No "inconsistent label cardinality" scrape failures. The unchecked Prometheus collector
also skips (rather than panics on) any sample whose key set would be inconsistent.
