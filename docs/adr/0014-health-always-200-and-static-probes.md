# `/livez` `/readyz`, and `/health` always answering 200

## Status

Accepted (2026-08-01). Additive. Does not supersede any prior ADR.

## Context

Same argument as obs_exporter's ADR-0013 and ADR-0014, applied here in one
pass: an exporter is a probe. "Server unreachable" is data it reports, not a
failure of the exporter process. Coupling that fact to an HTTP status code
on any endpoint — the chart's `livenessProbe`/`readinessProbe`, or the
informational `/health` — risks something downstream (kubelet, a dashboard,
a script) treating a healthy, correctly-reporting exporter as down.

`charts/ppdm-exporter/values.yaml` wired both `livenessProbe` and
`readinessProbe` to `/health`, which answered 503 while any configured
server was unreachable. As a *liveness* check this is always wrong: no
restart makes an unreachable server reachable. As a *readiness* check it
pulls the exporter from the scrape pool exactly when the down-server metric
is the fact worth scraping.

## Decision

Two new endpoints, `/livez` and `/readyz`, both `staticOKHandler` — always
`200 OK`, no `SnapshotStore` read, nothing that can make either fail once
the process is running. The chart's default probes now point at them.
`/health`'s `healthHandler` no longer writes `http.StatusServiceUnavailable`
— it always answers 200, with the same JSON body (`built_at`,
`servers: [{server, ok, last_scrape, err}]`) as before. The per-server
`ok`/`err` fields are the only status channel now; nothing that parses the
body loses information.

## Consequences

- Anything gating on `/health`'s HTTP status code now sees 200
  unconditionally and must read `ok`/`err` per server instead.
- Chart default probe wiring changes; a fresh `helm install` or an upgrade
  without pinned probe overrides gets the fix automatically.
- Alert on a per-server `_up` metric (or `/health`'s body), never on any
  probe's HTTP status.
