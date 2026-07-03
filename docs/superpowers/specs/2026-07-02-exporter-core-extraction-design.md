# Exporter family core extraction — design

**Status:** approved design, not started. Lives in `ppdm_exporter` as the family
reference repo; the work itself spans the whole exporter family.

## Context

Fred maintains 11 Go Prometheus + OTLP exporters (`idrac`, `nbu`, `nsr`, `obs`,
`pflex`, `pmax`, `ppdd`, `ppdm`, `pscale`, `pstore`, `pve`) plus the shared library
`licenses-exporter-core`, all standardized by the `exporter-standards` skill.
(`proxmox-exporter` is Starttoaster's upstream clone — out of scope.)

Four pains motivate this refactor (all confirmed):

1. **Cross-repo feature rollout** — every engine fix/feature is repeated in 11 repos by hand.
2. **Inconsistency/drift** — two `internal/` layout generations
   (older: `config/logging/models/telemetry/utils/<vendor>`;
   newer: `config/<vendor>/<vendor>client`), diverging Make targets, ADR naming.
3. **Boilerplate for new exporters** — scaffolding still copies ~30% engine code.
4. **Dependency/CI upkeep** — 11× dependabot, Go bumps, action pinning, govulncheck churn.

`licenses-exporter-core` already proved the shared-core pattern: its engine
(collection loop, `SnapshotStore`, dual Prometheus/OTLP export over a generic
`Sample{Name, Labels, Value}`, config helpers, health, validated hot reload) is
~90% vendor-neutral today — only its `metrics.go` constructors and the
`helpText`/`allMetricNames` maps are license-schema-specific. `ppdm`'s
`internal/ppdm` is structurally the same engine, re-rolled.

**Decision: Approach A** — keep all repos separate (own releases, GHCR images, docs
sites, Homebrew casks); extract a shared Go module + reusable CI workflows.
Rejected: monorepo (loses 11 established public repos/docs/release histories;
multiplexed GoReleaser/GHCR/Pages complexity); converge-without-sharing (doesn't
fix rollout or boilerplate).

## Deliverables

1. **`fjacquet/exporter-core`** — Go module with the vendor-neutral engine,
   extracted by generalizing `licenses-exporter-core` and reconciling against
   `ppdm`'s engine.
2. **`fjacquet/exporter-workflows`** — reusable GitHub workflows
   (`ci.yml`, `release.yml`, `docs.yml`) + composite actions, SHA-pinned in one
   place, consumed as `uses: fjacquet/exporter-workflows/.github/workflows/ci.yml@v1`.
3. **One pilot exporter (`ppdd_exporter`) migrated** onto both, behavior-preserving:
   existing tests pass unchanged; sorted `--once` `/metrics` dump byte-identical
   before/after.

Out of scope: migrating the other 10 repos (opportunistic follow-ups, each its own
PR + ADR), any metric or feature changes.

## `exporter-core` module API

Dividing line: **core owns the engine; exporters own the vendor client and the
metric catalog.**

| Package | Contents |
|---|---|
| `snapshot` | Generic `Sample{Name, Labels, Value}`, immutable `Snapshot`, pointer-swap `SnapshotStore`. The metric **schema registry** (help text, OTLP metric names, ordered label-key sets) becomes a `Catalog` the exporter constructs and passes in — not hardcoded maps as in licenses-core today. |
| `engine` | Collection loop (`CollectOnce`/`Run`), per-source parallelism, concurrency limit, timeout; driven by a `Source` interface each exporter implements per configured server. |
| `export` | Unchecked Prometheus collector + OTLP observable-gauge registration, both reading the store through the `Catalog`. |
| `config` | `LoadYAML`, `${ENV}` expansion, `ResolveSecret` (inline/`passwordFile`), dotenv, SIGHUP + fsnotify **validated cancelable hot reload**. |
| `server` | HTTP serving **before first collect**, `/metrics`, `/healthz` readiness. |
| `testkit` | Label-key-invariant helper (one ordered label-key set per metric name; rollups pad empty values), catalog round-trip checks. |

Explicitly **not** in core: retry/auth policy (lives in each vendor client —
`pstore`/`pscale` use vendor SDKs, others resty; a shared client abstraction is
where this would over-abstract), collectors, DTO structs, trace-redaction rules.

`licenses-exporter-core` becomes the first consumer: keeps its `license_` schema
and `Main`, deletes its engine copy. That is the proof the generalization is right.

## `exporter-workflows`

Each repo's three workflows shrink to thin callers passing inputs (image name,
mkdocs on/off; Go version read from `go.mod`). Action SHA-pinning, govulncheck,
GoReleaser invocation, and Pages deploy live in one repo; each consumer's
dependabot tracks the `@vX` tag. GoReleaser configs stay per-repo (they name
per-repo artifacts/casks) but converge on one canonical template documented in the
`exporter-standards` skill.

## Migration order

1. Extract `exporter-core` (from licenses-core + ppdm reconciliation);
   `licenses-exporter-core` consumes it.
2. **Pilot: `ppdd_exporter`** — newest-generation layout, no SDK, no report
   subsystem; cleanest single test of the seam.
3. `ppdm` (its `cmd/report` binary shares `internal/config` — second, not first),
   then `nsr`, `obs`, `pmax`.
4. Older-layout five (`nbu`, `pflex`, `pscale`, `pstore`, `pve`) — these also unify
   to the `config + <vendor> + <vendor>client` layout as part of migration.
   `idrac` last (bespoke layout).
5. Each migration: own PR + ADR in that repo; progress tracked in the
   `exporter-standards` drift table.

## Versioning, testing, risk

- Core starts at `v0.x`; cut `v1` once three exporters consume it, then semver API
  discipline. Dependabot fans out bumps.
- Core carries the tests extracted with the code plus `testkit`. Migration gate per
  repo: unchanged test suite green + before/after sorted `/metrics` diff clean +
  `make ci`.
- **Risk — over-abstraction:** anything needing a vendor-specific branch inside
  core gets pushed back to the exporter. **Risk — breaking-change fan-out:**
  mitigated by semver and opportunistic migration pace (consumers pin old versions
  and keep working).
