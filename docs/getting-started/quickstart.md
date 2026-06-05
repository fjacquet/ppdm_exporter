# Quick start

```bash
make cli
export PPDM01_PASSWORD='...'
./bin/ppdm_exporter --config config.yaml --debug
```

- Metrics: <http://localhost:9102/metrics>
- Health: <http://localhost:9102/health> (`200` when all servers are OK, `503` otherwise)

Scrape it from Prometheus:

```yaml
scrape_configs:
  - job_name: ppdm
    static_configs:
      - targets: ['ppdm-host:9102']
```

Run a single cycle without serving (useful to validate credentials/connectivity):

```bash
./bin/ppdm_exporter --once --config config.yaml --debug
```

No PPDM handy? Use the [end-to-end demo](../deployment/compose-demo.md) — `make demo` spins
up a mock PPDM server, the exporter, Prometheus, and Grafana.
