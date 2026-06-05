# Configuration

The exporter reads a YAML file (default `config.yaml`, override with `--config`).

```yaml
server:
  host: "0.0.0.0"
  port: "9102"
  uri: "/metrics"
collection:
  interval: "5m"            # snapshot cadence
  timeout: "60s"            # per-server cycle timeout
  lookback: "24h"           # activities query window (createdAt ge ...)
  assetAgeThreshold: "24h"  # emit per-asset last-copy-age only past this age
  perJobActivities: false   # emit per-job activity metrics (backup-report table; higher cardinality)
otel:
  enabled: false            # set true to push metrics over OTLP gRPC
  endpoint: "localhost:4317"
  insecure: true
  interval: "30s"
servers:
  - name: ppdm-prod-01
    host: ppdm01.example.com
    port: 8443                       # PPDM REST API port (default 8443)
    username: ppdm-monitor
    password: "${PPDM01_PASSWORD}"   # ${ENV} interpolation
    insecureSkipVerify: true         # accept self-signed PPDM certs
```

## Secrets

- `${ENV_VAR}` references are interpolated from the environment; an unset variable is a
  load-time error (fail fast, not a silent auth failure).
- Alternatively set `passwordFile: /run/secrets/ppdm01` to read the password from a file.

## Hot reload

The config reloads on **`SIGHUP`** or a file change — clients and the collection loop are
rebuilt and swapped with no downtime. A malformed edit is logged and ignored; the running
config stays in effect.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--config` | `config.yaml` | Config file path. |
| `--once` | `false` | Run a single collection cycle and exit (diagnostics). |
| `--debug` | `false` | Verbose logging. |
