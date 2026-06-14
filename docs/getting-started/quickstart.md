# Quick start

```bash
make cli
export PPDM1_PASSWORD='...'
./bin/ppdm_exporter --config config.yaml --debug
```

- Metrics: <http://localhost:9442/metrics>
- Health: <http://localhost:9442/health> (`200` when all servers are OK, `503` otherwise)

Scrape it from Prometheus:

```yaml
scrape_configs:
  - job_name: ppdm
    static_configs:
      - targets: ['ppdm-host:9442']
```

Run a single cycle without serving (useful to validate credentials/connectivity):

```bash
./bin/ppdm_exporter --once --config config.yaml --debug
```

Useful flags:

- `--once` — run a single collection cycle and exit.
- `--debug` — verbose logging. Combined with `--once`, it also prints **every
  collected sample** (sorted, exposition style) so you can diff a live appliance
  against the [metrics reference](../metrics.md).
- `--trace` — log every PPDM API response body (method, URL, status, payload; the
  login exchange is skipped, so the access token is never logged). Use it when a
  metric you expect is absent: the exporter never guesses values, so an unexpected
  payload shape shows up as a missing sample — the trace shows what the appliance
  actually returned.

Validating against a real appliance:

```bash
./bin/ppdm_exporter --config config.yaml --once --debug --trace 2>trace.log | sort > samples.txt
# samples.txt  → every metric collected (compare with docs/metrics.md)
# trace.log    → raw API payloads for anything missing or suspicious
```

No PPDM handy? Use the [end-to-end demo](../deployment/compose-demo.md) — `make demo` spins
up a mock PPDM server, the exporter, Prometheus, and Grafana.
