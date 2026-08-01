# End-to-end demo (Compose)

A self-contained stack that needs **no real PPDM hardware**:

```
mockppdm → ppdm_exporter → Prometheus → Grafana
```

`cmd/mockppdm` is a tiny fake PPDM server that serves canned fixtures over self-signed TLS
(bearer login + the six list endpoints), so the dashboard populates immediately.

## Run

```bash
make demo            # docker compose up --build
```

| Service | URL | Notes |
|---|---|---|
| Grafana | <http://localhost:3000> | `admin` / `admin` → *PowerProtect Data Manager — Overview* |
| Prometheus | <http://localhost:9090> | target `ppdm_exporter` should be **up** |
| Exporter | <http://localhost:9442/metrics> | raw metrics |

Tear down:

```bash
make demo-down       # docker compose down --remove-orphans
```

## What you should see

`ppdm_up = 1`, activity/asset/alert/capacity panels populated, and the *Assets at risk*
panel showing the one stale demo asset (`nas01`) — the bounded SLA-age logic in action.

## Running from the published image

`docker-compose.ghcr.yml` mirrors the same seven-service stack, but pulls
`ppdm_exporter` from GHCR instead of building it from source (`mockppdm` and `report` are
still built locally — they're demo-only and never published):

```bash
docker compose -f docker-compose.ghcr.yml up -d
```

Pin a version with `PPDM_TAG` (defaults to `:latest`):

```bash
PPDM_TAG=3.0.0 docker compose -f docker-compose.ghcr.yml up -d
```
