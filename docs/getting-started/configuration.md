# Configuration

The exporter reads a YAML file (default `config.yaml`, override with `--config`).

```yaml
server:
  host: "0.0.0.0"
  port: "9442"
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
    host: "${PPDM1_HOSTNAME}"       # ${ENV} interpolation supported
    port: 8443                       # PPDM REST API port (default 8443)
    username: "${PPDM1_USERNAME}"   # ${ENV} interpolation supported
    password: "${PPDM1_PASSWORD}"   # ${ENV} interpolation supported
    insecureSkipVerify: true         # accept self-signed PPDM certs
```

## Secrets and environment interpolation

`${ENV_VAR}` references are interpolated from the environment in `host`, `username`,
and `password` fields. An unset variable is a load-time error — fail fast rather than
a silent auth failure at collection time.

Alternatively set `passwordFile: /run/secrets/ppdm01` to read the password from a file.

**Single-server convenience:** For a single server, you can drive the entire identity
from environment variables (e.g. from a secrets manager or `.env` file) without editing
`config.yaml`. Copy `.env.example` to `.env`, fill in the values, and `docker-compose`
will export them automatically.

**Multi-server deployments:** `config.yaml` is the source of truth — add one entry
per server under `servers:`. Each entry may reference its own distinct env vars
(e.g. `${PPDM2_PASSWORD}`, `${PPDM03_HOSTNAME}`).

### .env loading

The exporter binary loads a `.env` file natively at startup — from the working
directory first, then next to the config file — so `cp .env.example .env` works
for bare-metal and systemd runs exactly like it does under docker compose.
Already-set environment variables **always take precedence** over `.env` values,
so secret injection (systemd `Environment=`, Kubernetes secrets, CI) can never be
shadowed by a stray file.

`config.yaml` is always the source of truth and is always consumed. For
**multi-server** setups use one `servers` entry per server, either with literal
values or with per-server env refs (e.g. `${PPDM1_PASSWORD}`, `${PPDM2_PASSWORD}`)
— there is no implicit discovery of servers from env vars.

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
