# Snapshot collection model

## Status
Accepted.

## Context
PPDM API load must be decoupled from the number of Prometheus scrapers and the OTLP push
cadence. Fetching on every scrape would multiply login + list calls and risk timeouts.
Family precedent: `ppdd` 0001, `pstore` 0002.

## Decision
A single background **collection loop** polls every configured server on
`collection.interval`, runs the modular `ResourceCollector`s in parallel (`errgroup`),
builds an **immutable `Snapshot`**, and pointer-swaps it into a `SnapshotStore` (RWMutex).
Both export paths read the latest snapshot rather than fetching on scrape:

```
loop → login → [activities | assets | capacity | health] → Snapshot → SnapshotStore.Swap()
                                                              ├── PromCollector (/metrics)
                                                              └── OTLPExporter (push)
```

Per-server failures degrade gracefully (`ppdm_up=0`, `ppdm_collector_up=0`) without failing
the cycle or other servers.

## Consequences
Backend load is constant regardless of scraper count. Metrics are at most one interval
stale. The snapshot exposes `MetricNames()`/`SamplesByName()` so OTLP observable gauges
read the same data Prometheus serves.
