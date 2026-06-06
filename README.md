# ppdm_exporter

[![CI](https://github.com/fjacquet/ppdm_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/fjacquet/ppdm_exporter/actions/workflows/ci.yml)
[![Release](https://github.com/fjacquet/ppdm_exporter/actions/workflows/release.yml/badge.svg)](https://github.com/fjacquet/ppdm_exporter/actions/workflows/release.yml)
[![Docs](https://github.com/fjacquet/ppdm_exporter/actions/workflows/docs.yml/badge.svg)](https://github.com/fjacquet/ppdm_exporter/actions/workflows/docs.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/fjacquet/ppdm_exporter.svg)](https://pkg.go.dev/github.com/fjacquet/ppdm_exporter)
[![Go Report Card](https://goreportcard.com/badge/github.com/fjacquet/ppdm_exporter)](https://goreportcard.com/report/github.com/fjacquet/ppdm_exporter)
[![Go Version](https://img.shields.io/github/go-mod/go-version/fjacquet/ppdm_exporter)](go.mod)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A Prometheus + OTLP exporter for **Dell PowerProtect Data Manager (PPDM)**. One
process polls many PPDM servers on an interval, publishes an immutable snapshot, and
serves it at `/metrics` while optionally pushing the same snapshot over OTLP.

Part of Fred's family of Dell storage/backup exporters (alongside `ppdd`, `pflex`,
`pstore`, `pscale`, `nbu`); built to the shared `exporter-standards`.

## Quick start

```bash
# Build
make cli

# Run against a PPDM server (set the password via env)
export PPDM01_PASSWORD=...
./bin/ppdm_exporter --config config.yaml --debug
# metrics on http://localhost:9102/metrics, health on /health
```

### End-to-end demo (no PPDM required)

```bash
make demo        # mockppdm -> exporter -> Prometheus -> Grafana
# Grafana:    http://localhost:3000  (admin/admin) -> "PowerProtect Data Manager — Overview"
# Prometheus: http://localhost:9090
# Exporter:   http://localhost:9102/metrics
make demo-down
```

The demo uses `cmd/mockppdm`, a tiny fake PPDM server that returns canned fixtures, so
the dashboard populates without real hardware.

## Configuration

`config.yaml` (secrets via `${ENV_VAR}` or `passwordFile`):

```yaml
server: {host: "0.0.0.0", port: "9102", uri: "/metrics"}
collection:
  interval: "5m"           # snapshot cadence
  timeout: "60s"           # per-server cycle timeout
  lookback: "24h"          # activities query window
  assetAgeThreshold: "24h" # emit per-asset last-copy-age only past this age
otel: {enabled: false, endpoint: "localhost:4317", insecure: true, interval: "30s"}
servers:
  - {name: ppdm-prod-01, host: ppdm01.example.com, port: 8443,
     username: ppdm-monitor, password: "${PPDM01_PASSWORD}", insecureSkipVerify: true}
```

Config hot-reloads on `SIGHUP` or file change.

## Metrics

See [`docs/metrics.md`](docs/metrics.md). Highlights: `ppdm_activity_count`,
`ppdm_activity_bytes_total`, `ppdm_asset_count`, `ppdm_asset_unprotected`,
`ppdm_asset_last_copy_age_seconds`, `ppdm_storage_{unit,system}_*_bytes`,
`ppdm_health_entity_status`, `ppdm_alert_count`, plus `ppdm_up` / `ppdm_collector_up`.

Every metric carries a `server` label, so one exporter serves many PPDM servers.

## Development

```bash
make ci       # gofmt, vet, golangci-lint, go test -race, govulncheck, build
make sure     # quick local gate (fmt, vet, test, build)
make test     # unit tests
```

> **API shapes are provisional.** Modeled from the PPDM 19.22.0 REST API reference and
> cross-checked against the Apache-2.0 `dell/powerprotect-data-manager` module. Capacity
> and health-entities fields are unconfirmed by any source — see `docs/adr/0009`.

## License

Apache-2.0.
