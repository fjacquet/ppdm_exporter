# ppdm_exporter — project guide

A Prometheus + OTLP exporter for Dell PowerProtect Data Manager (PPDM). Member of the
family standardized by the `exporter-standards` skill; mirrors the hand-rolled sibling
`ppdd_exporter` and `pflex_exporter`'s OTLP path.

## Commands

```bash
make ci        # the gate: gofmt, vet, golangci-lint, go test -race, govulncheck, build
make sure      # quick local gate: fmt, vet, test, build
make test      # unit tests          make cli     # build bin/ppdm_exporter
make demo      # docker-compose: mockppdm -> exporter -> Prometheus -> Grafana
make release-snapshot   # GoReleaser dry-run
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

## Load-bearing constraints

- **API shapes are provisional** — modeled from the 19.22.0 reference, cross-checked vs the
  Apache-2.0 `dell/powerprotect-data-manager` module. Every unverified field is tagged
  `// provisional`; `grep -rn provisional internal/` is the validation surface (ADR-0009).
  One struct + one fixture per shape: fixing a wrong field touches one file.
- **Label-key invariant**: one ordered label-key set per metric name; rollups pad empty
  values; per-object metrics get their own name. Enforced by `internal/ppdm/labels_test.go`.
- **Retry excludes 4xx**; re-login on expiry + 401. **Serve HTTP before first collect.**
- **No inline semgrep/lint suppressions** — restructure (e.g. `writeBytes(io.Writer, …)`).
- Per-second/windowed values are **gauges**, aggregated with `sum`/`max`, never `rate()`.

## Testing

TDD. Collectors are driven by the in-memory `ppdmclient.Mock`; client/auth/pagination by
`httptest`. Assert via both the Prometheus registry and an OTLP `ManualReader`. Keep
semgrep + golangci-lint clean.

## CI/CD

CI trio (`.github/workflows/ci.yml|release.yml|docs.yml`), SHA-pinned actions + dependabot,
GoReleaser v2 (binaries + CycloneDX SBOM + Homebrew cask), multi-arch GHCR image with
attestations, MkDocs Material → GitHub Pages. Everything CI runs is a Makefile target.

---

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (60-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk go test             # Go test failures only (90%)
rtk jest                # Jest failures only (99.5%)
rtk vitest              # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk pytest              # Python test failures only (90%)
rtk rake test           # Ruby test failures only (90%)
rtk rspec               # RSpec test failures only (60%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%). Format flags (-c, -l, -L, -o, -Z) run raw.
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->
