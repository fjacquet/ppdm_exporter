# ppdm_exporter — project guide

A Prometheus + OTLP exporter for Dell PowerProtect Data Manager (PPDM). Member of the
family standardized by the `exporter-standards` skill; mirrors the hand-rolled sibling
`ppdd_exporter` and `pflex_exporter`'s OTLP path.

## Commands

```bash
make ci        # the gate: gofmt, vet, golangci-lint, go test -race, govulncheck, build
make sure      # quick local gate: fmt, vet, test, build
make test      # unit tests          make build   # build bin/ppdm_exporter
make demo      # docker-compose: mockppdm -> exporter -> Prometheus -> Grafana
make release-snapshot   # GoReleaser dry-run

# Live-appliance validation: sorted sample dump (stdout) + raw API payloads (stderr)
./bin/ppdm_exporter --config config.yaml --once --debug --trace 2>trace.log | sort > samples.txt
```

## Architecture

Snapshot collection model. A background loop (`internal/ppdm/collector.go`) logs into each
configured server, runs four modular `ResourceCollector`s in parallel, and pointer-swaps an
immutable `Snapshot` into a `SnapshotStore`. Both export paths read the snapshot:
`internal/ppdm/prometheus.go` (unchecked collector) and `internal/ppdm/otlp.go` (observable
gauges). Client + auth + pagination live in `internal/ppdmclient`. See `docs/adr/`.

- Collectors: `activities.go`, `assets.go`, `capacity.go`, `health.go` — each owns one
  endpoint + JSON struct so provisional-API risk is localized.
- `internal/config` — YAML load, `${ENV}`/`passwordFile`, SIGHUP + fsnotify hot reload.
- `cmd/mockppdm` — fake PPDM server (fixtures over TLS) for the demo stack.

### cmd/report (backup history + assurance reports)

A second binary capturing authoritative PPDM records (activities/copies/assets/policies)
into PostgreSQL, and serving/delivering assurance reports from it. Reuses the shared `internal/ppdmclient.GetAll`
(no client change) + `internal/config` (`LoadReport`); `internal/report` holds the capture
DTOs, the `pgx` `Store` (embedded `migrations.sql`, idempotent upserts, watermark, prune,
`capture_runs` provenance), and the `Capturer` loop. DB tests use `testcontainers-go`
(skipped in `-short`). Grafana reads Postgres directly (BackupHistory datasource) for the
global-search view. `report/render` builds the HTML/PDF report behind a read-only
`GET /report` with per-tenant bearer authz (ADR-0012); `report/delivery` sends it by SMTP;
`report/schedule` runs scheduled deliveries. Retention is per tenant (ADR-0013). Assurance,
not forensic: no crypto/audit machinery. The exporter binary and image are untouched.

## Load-bearing constraints

- **API shapes are provisional** — modeled from the 19.22.0 reference, cross-checked vs the
  Apache-2.0 `dell/powerprotect-data-manager` module. Every unverified field is tagged
  `// provisional`; `grep -rn provisional internal/` is the validation surface (ADR-0009).
  One struct + one fixture per shape: fixing a wrong field touches one file.
- **Label-key invariant**: one ordered label-key set per metric name; rollups pad empty
  values; per-object metrics get their own name. Enforced by `internal/ppdm/labels_test.go`.
- **Retry excludes 4xx**; re-login on expiry + 401. **Serve HTTP before first collect.**
- **Trace never logs tokens**: `ppdmclient.Config.Trace` logs response **bodies only** via
  an `OnAfterResponse` hook and skips `POST /api/v2/login` — PPDM returns the access_token
  *in that response body*. Never use resty `SetDebug` (dumps auth headers + login body).
- **No inline semgrep/lint suppressions** — restructure (e.g. `writeBytes(io.Writer, …)`).
- Per-second/windowed values are **gauges**, aggregated with `sum`/`max`, never `rate()`.

## Testing

TDD. Collectors are driven by the in-memory `ppdmclient.Mock`; client/auth/pagination by
`httptest`. Assert via both the Prometheus registry and an OTLP `ManualReader`. Keep
semgrep + golangci-lint clean.

## CI/CD

CI (`.github/workflows/`: `ci.yml`, `release.yml`, `docs.yml`, `helm-charts.yml`,
`dependabot-automerge.yml`), SHA-pinned actions + dependabot,
GoReleaser v2 (binaries + CycloneDX SBOM + Homebrew cask), multi-arch GHCR image with
attestations, MkDocs Material → GitHub Pages. Everything CI runs is a Makefile target.
