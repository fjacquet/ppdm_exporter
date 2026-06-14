# Metric naming & units

## Status
Accepted.

## Context
Consistent, unit-explicit names and a single identity label let one exporter serve many
servers and keep PromQL portable. Family precedent: `pstore` 0006, `pstore` 0005.

## Decision
- Prefix all metrics `ppdm_`; listen port **9442**.
- Every metric carries a `server` identity label (one process, many PPDM servers).
- Be unit-explicit: byte metrics end `_bytes`, ages end `_seconds`.
- **Per-second / rate values are gauges**, aggregated with `sum`/`avg`, never `rate()`.
  v1 emits no per-second metric — the only candidate (`bytesTransferredPerSecond`, summed
  across jobs) was dropped as semantically unsound; cumulative `result.bytesTransferred` is
  summed into `ppdm_activity_bytes_total` instead.
- Counts that slide with a window (`ppdm_activity_count`, `ppdm_activity_bytes_total`) are
  gauges recomputed each cycle, not counters.

## Consequences
Names are stable and self-describing. Dashboards aggregate with `sum`/`max` and never apply
`rate()` to already-aggregated values.
