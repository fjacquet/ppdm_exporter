# Standardize container base image on Alpine

## Status

Accepted (2026-08-01)

## Context

The exporter family had two published-image patterns — Alpine (5 repos) and
`gcr.io/distroless/static:nonroot` (this repo and 2 others: pmax_exporter,
ppdd_exporter) — as undocumented per-repo author choice, with no written
criterion. Alpine has a shell and `wget`, so it can carry a Docker `HEALTHCHECK`
pointed at `/livez` (already wired in `main.go`); distroless cannot.

## Decision

Both `Dockerfile` and `Dockerfile.goreleaser` move from
`gcr.io/distroless/static:nonroot` to `alpine:latest`. Named user `ppdm`, uid
`10001` (was `nonroot:nonroot`/`65532`). `HEALTHCHECK`/`healthcheck:` against
`/livez` via `127.0.0.1` (never `localhost` — Alpine's busybox `wget` resolves
`localhost` via `::1` first, and the exporter only binds IPv4). The
previously-missing `docker-compose.ghcr.yml` is added at the same time,
preserving the full six-service demo topology (postgres, mailpit, report).

## Consequences

- **Breaking**: the published image's container UID changes from `65532` to
  `10001`. Checked this repo's Helm chart (`charts/ppdm-exporter/values.yaml`)
  for a hardcoded `runAsUser`/`fsGroup` referencing the old UID — none found;
  the chart's security-context fields are commented-out generic defaults,
  never active, so no chart change is required.
- The image gains a shell and `apk` — larger attack surface, larger image —
  accepted family-wide as the trade for `HEALTHCHECK` and shell-based
  debuggability.
- The full family standard and per-repo work breakdown live in
  `obs_exporter`'s `docs/superpowers/specs/2026-08-01-alpine-standard-design.md`.
