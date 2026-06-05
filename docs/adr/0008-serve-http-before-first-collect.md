# Serve HTTP before first collect

## Status
Accepted.

## Context
The first collection cycle includes login + list calls to every server and can exceed a
scrape/health timeout. Blocking startup on it would stall `/metrics` and `/health`, making
the exporter look down during its own warm-up. Family precedent: `pstore` 0007.

## Decision
`NewSnapshotStore()` pre-populates an empty snapshot so readers never see nil. `main` starts
the HTTP server **before** the first `CollectOnce`, then runs the loop. Until the first cycle
completes, `/metrics` returns only the empty set and `/health` reports no servers (503).

## Consequences
`/metrics` is reachable immediately; a slow first poll never makes the process appear dead.
`--once` mode skips serving and just runs a single cycle for diagnostics.
