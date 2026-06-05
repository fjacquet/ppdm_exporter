# ppdm_exporter

A Prometheus + OTLP exporter for **Dell PowerProtect Data Manager (PPDM)**. One process
polls many PPDM servers on an interval, publishes an immutable snapshot, and serves it at
`/metrics` while optionally pushing the same snapshot over OTLP.

Part of a family of Dell storage/backup exporters built to a shared standard.

## What it exports

- **Backup activity** — job outcome counts and data volume by category (`PROTECT`, `RESTORE`, …).
- **Asset protection** — coverage by type and status, plus bounded per-asset SLA-age for
  at-risk assets.
- **Capacity** — MTree and storage-system bytes.
- **Health & alerts** — health-entity status and open alert counts by severity.
- **Liveness** — `ppdm_up` / `ppdm_collector_up` per server and per domain.

## How it works

A single background loop logs into each server, runs modular per-domain collectors in
parallel, and pointer-swaps an immutable snapshot that both export paths read — so backend
load is independent of scraper count. See [the snapshot ADR](adr/0002-prometheus-snapshot-model.md).

## Next steps

- [Installation](getting-started/installation.md)
- [Configuration](getting-started/configuration.md)
- [Quick start](getting-started/quickstart.md)
- [Metrics reference](metrics.md)
- [End-to-end demo](deployment/compose-demo.md)

!!! warning "Provisional API shapes"
    Built without a live PPDM server; JSON field shapes are modeled from the 19.22.0 API
    reference and cross-checked against the Apache-2.0 `dell/powerprotect-data-manager`
    module. Capacity and health-entities fields are unconfirmed — see
    [ADR-0009](adr/0009-provisional-api-mappings.md).
