# Alpine Standard — ppdm_exporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert ppdm_exporter's published and local container images from `gcr.io/distroless/static:nonroot` to Alpine, matching the family standard, and add the `HEALTHCHECK`/`healthcheck:` this unlocks. Also close a pre-existing, unrelated gap: this repo has no `docker-compose.ghcr.yml`.

**Architecture:** Both `Dockerfile` and `Dockerfile.goreleaser` swap their final `FROM gcr.io/distroless/static:nonroot` stage for the family's canonical Alpine runtime stage; builder stages are untouched. `docker-compose.yml` gains `healthcheck:` on `ppdm_exporter`. `docker-compose.ghcr.yml` is created new: same six-service stack (`mockppdm`, `ppdm_exporter`, `prometheus`, `grafana`, `postgres`, `mailpit`, `report`), only `ppdm_exporter` switches from build to pull — `mockppdm` and `report` stay build-only (demo-only, never published, matching `obs_exporter`'s `mockecs` and `ppdd_exporter`'s `mockdd` convention).

**Tech Stack:** Docker, Alpine (`wget`/busybox), Go 1.26.5.

**Spec:** `docs/superpowers/specs/2026-08-01-alpine-standard-design.md` in `obs_exporter` (family-wide design).

## Global Constraints

- `HEALTHCHECK`/`healthcheck:` target `http://127.0.0.1:9442/livez`, never `localhost` — Alpine's busybox `wget` resolves `localhost` via `::1` first, and the exporter only binds IPv4.
- Timing: `--interval=30s --timeout=5s --start-period=10s --retries=3`.
- Builder stages do not change — only the final `FROM` and everything after it.
- Uid `10001`, named user `ppdm` (was `nonroot:nonroot`/`65532`) — **breaking change** for the published image; no Helm chart impact (confirmed: `charts/ppdm-exporter/values.yaml`'s `runAsUser`/`fsGroup` are commented-out generic defaults, never active).
- `/livez` and `/readyz` are already wired in `main.go` — confirmed, no Go code changes needed.
- `docker-compose.ghcr.yml` must preserve the full six-service topology of `docker-compose.yml` (postgres healthcheck, mailpit, the `report` service's `depends_on: condition: service_healthy` on postgres) — this is not a simplified variant.
- No inline `nosemgrep`/`//nolint` suppressions.
- `make ci` must stay green.

## File Structure

| File | Responsibility |
| --- | --- |
| `Dockerfile` | Rewrite runtime stage: distroless → Alpine, add `HEALTHCHECK` |
| `Dockerfile.goreleaser` | Rewrite runtime stage: distroless → Alpine, add `HEALTHCHECK` |
| `docker-compose.yml` | Add `healthcheck:` to `ppdm_exporter` |
| `docker-compose.ghcr.yml` | New file — pull-based variant, full topology |
| `docs/adr/000N-alpine-standard.md` | Records the decision (breaking) |
| `CHANGELOG.md` | `Breaking` entry |

---

### Task 1: Rewrite the local ./Dockerfile to Alpine

**Files:**
- Modify: `Dockerfile`

**Interfaces:** none.

- [ ] **Step 1: Replace the runtime stage**

Current file:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/ppdm_exporter .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/ppdm_exporter /ppdm_exporter
USER nonroot:nonroot
EXPOSE 9442
ENTRYPOINT ["/ppdm_exporter"]
CMD ["--config", "/etc/ppdm_exporter/config.yaml"]
```

Replace with:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/ppdm_exporter .

FROM alpine:latest

# Create the runtime user and log dir. These are busybox builtins (no network).
RUN adduser -D -u 10001 ppdm && \
    mkdir -p /var/log/ppdm_exporter && \
    chown ppdm:ppdm /var/log/ppdm_exporter

# Copy the CA bundle from the builder stage instead of `apk add ca-certificates`.
# The latter fetches from the Alpine CDN over TLS, which fails behind a corporate
# MITM proxy: the bare alpine image has no CA bundle yet to validate the proxy
# cert (chicken-and-egg). The Debian-based golang builder already ships the bundle.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=build /out/ppdm_exporter /usr/bin/ppdm_exporter
COPY config.yaml /etc/ppdm_exporter/config.yaml

EXPOSE 9442

# /livez never depends on target reachability or the collection cycle, so it
# can never flag a healthy process as down over an unreachable PPDM instance
# (see ADR-000N).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:9442/livez || exit 1

USER ppdm

ENTRYPOINT ["/usr/bin/ppdm_exporter"]
CMD ["--config", "/etc/ppdm_exporter/config.yaml"]
```

The local image now bakes `config.yaml` in — it didn't before. Intentional, additive.

- [ ] **Step 2: Lint**

Run: `hadolint Dockerfile`
Expected: no findings on the lines just added.

- [ ] **Step 3: Build and verify at runtime**

```bash
docker build -t ppdm_exporter:alpine-test .
docker run -d --name ppdm-hc-test -p 19442:9442 \
  -v "$(pwd)/config.demo.yaml:/etc/ppdm_exporter/config.yaml:ro" \
  ppdm_exporter:alpine-test
sleep 15
docker inspect --format='{{.State.Health.Status}}' ppdm-hc-test
docker exec ppdm-hc-test whoami
```

Expected: `healthy`, `whoami` prints `ppdm`.

- [ ] **Step 4: Clean up test artifacts**

```bash
docker rm -f ppdm-hc-test
docker rmi ppdm_exporter:alpine-test
```

- [ ] **Step 5: Commit**

```bash
git add Dockerfile
git commit -m "feat(docker)!: rewrite local Dockerfile to Alpine (was distroless)

BREAKING CHANGE: container UID changes from 65532 (nonroot) to 10001 (named user ppdm)."
```

---

### Task 2: Rewrite Dockerfile.goreleaser to Alpine

**Files:**
- Modify: `Dockerfile.goreleaser`

**Interfaces:** none.

- [ ] **Step 1: Replace the file**

Current file:

```dockerfile
# Release image: copies the prebuilt GoReleaser binary (no in-image compile).
# buildx lays the cross-compiled binary out per-platform as
# ${TARGETPLATFORM}/ppdm_exporter in the build context.
FROM gcr.io/distroless/static:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/ppdm_exporter /ppdm_exporter
COPY config.yaml /etc/ppdm_exporter/config.yaml
USER nonroot:nonroot
EXPOSE 9442
ENTRYPOINT ["/ppdm_exporter"]
CMD ["--config", "/etc/ppdm_exporter/config.yaml"]
```

Replace with:

```dockerfile
# Release image: copies the prebuilt GoReleaser binary (no in-image compile).
# buildx lays the cross-compiled binary out per-platform as
# ${TARGETPLATFORM}/ppdm_exporter in the build context.
FROM alpine:latest

RUN apk --no-cache add ca-certificates && \
    adduser -D -u 10001 ppdm && \
    mkdir -p /var/log/ppdm_exporter && \
    chown ppdm:ppdm /var/log/ppdm_exporter

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/ppdm_exporter /usr/bin/ppdm_exporter
COPY config.yaml /etc/ppdm_exporter/config.yaml

EXPOSE 9442

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:9442/livez || exit 1

USER ppdm

ENTRYPOINT ["/usr/bin/ppdm_exporter"]
CMD ["--config", "/etc/ppdm_exporter/config.yaml"]
```

- [ ] **Step 2: Lint**

Run: `hadolint Dockerfile.goreleaser`
Expected: no new findings.

- [ ] **Step 3: Build and verify at runtime**

```bash
CGO_ENABLED=0 go build -o ppdm_exporter .
mkdir -p linux/amd64 && cp ppdm_exporter linux/amd64/ppdm_exporter
docker build -f Dockerfile.goreleaser --build-arg TARGETPLATFORM=linux/amd64 -t ppdm_exporter:goreleaser-test .
docker run -d --name ppdm-gr-hc-test -p 19445:9442 \
  -v "$(pwd)/config.demo.yaml:/etc/ppdm_exporter/config.yaml:ro" \
  ppdm_exporter:goreleaser-test
sleep 15
docker inspect --format='{{.State.Health.Status}}' ppdm-gr-hc-test
```

Expected: `healthy`.

- [ ] **Step 4: Clean up test artifacts**

```bash
docker rm -f ppdm-gr-hc-test
docker rmi ppdm_exporter:goreleaser-test
rm -rf linux ppdm_exporter
```

- [ ] **Step 5: Commit**

```bash
git add Dockerfile.goreleaser
git commit -m "feat(docker)!: rewrite the published image to Alpine (was distroless)

BREAKING CHANGE: container UID changes from 65532 (nonroot) to 10001 (named user ppdm)."
```

---

### Task 3: Compose — add healthcheck, create the ghcr variant

**Files:**
- Modify: `docker-compose.yml`
- Create: `docker-compose.ghcr.yml`

**Interfaces:** none.

- [ ] **Step 1: Add healthcheck to docker-compose.yml**

In the `ppdm_exporter` service, after `restart: unless-stopped` (line 37):

```yaml
    depends_on:
      - mockppdm
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://127.0.0.1:9442/livez"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

- [ ] **Step 2: Create docker-compose.ghcr.yml**

Same six services as `docker-compose.yml`. Only `ppdm_exporter` changes from build to pull; `mockppdm` and `report` stay build-only (never published); `postgres`/`mailpit`/`grafana`/`prometheus` are unchanged pulled images in both files already.

```yaml
---
# End-to-end demo stack — exporter + backup reporter, exporter from the PUBLISHED
# GHCR image.
#   docker compose -f docker-compose.ghcr.yml up -d
# Open:
#   Grafana  http://localhost:3000 (admin/admin)
#            - "PowerProtect Data Manager — Overview"  (live exporter metrics)
#            - "PowerProtect — SLA Compliance"         (backup compliance over Postgres)
#   Report   http://localhost:9103/report?tenant=acme-corp  (&format=pdf for PDF)
#
# Only mockppdm and report are built locally — they are demo-only and never published.
# Pin a version with PPDM_TAG (defaults to :latest):
#   PPDM_TAG=3.0.0 docker compose -f docker-compose.ghcr.yml up -d
services:
  mockppdm:
    build:
      context: .
      dockerfile: deploy/mockppdm/Dockerfile
    image: ppdm_mockppdm
    pull_policy: build
    container_name: ppdm_mockppdm
    restart: unless-stopped

  ppdm_exporter:
    image: ghcr.io/fjacquet/ppdm_exporter:${PPDM_TAG:-latest}
    container_name: ppdm_exporter
    command: ["--config", "/etc/ppdm_exporter/config.yaml"]
    ports:
      - "9442:9442"
    volumes:
      - ./config.demo.yaml:/etc/ppdm_exporter/config.yaml:ro
    depends_on:
      - mockppdm
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://127.0.0.1:9442/livez"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s

  prometheus:
    image: prom/prometheus:latest
    container_name: ppdm_prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
    depends_on:
      - ppdm_exporter
    restart: unless-stopped

  grafana:
    image: grafana/grafana:latest
    container_name: ppdm_grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning:ro
      - ./grafana/dashboards:/var/lib/grafana/dashboards:ro
    depends_on:
      - prometheus
      - postgres
    restart: unless-stopped

  # --- Backup history (cmd/report) ---
  postgres:
    image: postgres:17-alpine
    container_name: ppdm_postgres
    environment:
      - POSTGRES_USER=report
      - POSTGRES_PASSWORD=report
      - POSTGRES_DB=backup_report
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U report -d backup_report"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s
    restart: unless-stopped

  mailpit:
    image: axllent/mailpit:latest
    container_name: ppdm_mailpit
    ports:
      - "8025:8025"
    restart: unless-stopped

  report:
    build:
      context: .
      dockerfile: deploy/report/Dockerfile
    image: ppdm_report
    pull_policy: build
    container_name: ppdm_report
    command: ["--config", "/etc/report/config.report.yaml", "--debug"]
    ports:
      - "9103:9103"
    volumes:
      - ./config.report.demo.yaml:/etc/report/config.report.yaml:ro
    depends_on:
      mockppdm:
        condition: service_started
      postgres:
        condition: service_healthy
      mailpit:
        condition: service_started
    restart: unless-stopped
```

- [ ] **Step 3: Validate**

Run: `docker compose -f docker-compose.yml config -q && docker compose -f docker-compose.ghcr.yml config -q`
Expected: both exit 0.

- [ ] **Step 4: Smoke-test docker-compose.yml**

```bash
docker compose up -d --build mockppdm ppdm_exporter
sleep 20
docker inspect --format='{{.State.Health.Status}}' $(docker compose ps -q ppdm_exporter)
docker compose down
```

Expected: `healthy`.

- [ ] **Step 5: Commit**

```bash
git add docker-compose.yml docker-compose.ghcr.yml
git commit -m "feat(docker): add healthcheck; add the missing docker-compose.ghcr.yml"
```

---

### Task 4: ADR + CHANGELOG

**Files:**
- Create: `docs/adr/000N-alpine-standard.md`
- Modify: `CHANGELOG.md`

**Interfaces:** none.

- [ ] **Step 1: Find the next ADR number**

Run: `ls docs/adr/ | sort -V | tail -3`

- [ ] **Step 2: Write the ADR**

```markdown
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
```

- [ ] **Step 3: Add the CHANGELOG entry**

Under `## [Unreleased]`:

```markdown
### Breaking

- The published Docker image's base changes from
  `gcr.io/distroless/static:nonroot` to `alpine:latest`. The container UID
  changes from `65532` to a named user at `10001`. If you pin `runAsUser`,
  `fsGroup`, or similar in your own deployment manifests against the old UID,
  update it. See ADR-000N.

### Added

- `HEALTHCHECK` on both images, checking `/livez`.
- `docker-compose.ghcr.yml` — was missing; pulls the published exporter image
  while keeping `mockppdm`/`report` built locally, matching
  `docker-compose.yml`'s full six-service topology.
```

- [ ] **Step 4: Commit**

```bash
git add docs/adr/000N-alpine-standard.md CHANGELOG.md
git commit -m "docs: record ADR-000N (Alpine standard, breaking UID change)"
```

---

### Task 5: Full gate

- [ ] **Step 1: Run the CI gate**

Run: `make ci`
Expected: clean.

- [ ] **Step 2: Commit any fixes**

```bash
git commit -am "fix: address CI gate findings for the Alpine standard change"
```

(Skip if clean.)

## Self-Review

- Spec coverage: ppdm_exporter's row (full conversion, both Dockerfiles; healthcheck on `.yml`; create missing `.ghcr.yml` preserving the full topology) — Tasks 1–3. Documentation — Task 4.
- No placeholders: ADR number confirmed by a one-command check; `docker-compose.ghcr.yml` content is the actual full six-service stack, not a stub.
- Breaking change called out explicitly in commits and CHANGELOG.
- Scope: single repo; matches the family plan's per-repo row exactly.
