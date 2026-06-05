# CI & supply-chain hardening

## Status
Accepted.

## Context
The exporter is distributed as binaries and a container image. Supply-chain integrity
(pinned dependencies, reproducible builds, vulnerability scanning, SBOMs) is a family
requirement (`pflex` 0001, `ppdd`/`pstore` 0008, `pscale` 0001).

## Decision
- Every GitHub Action is pinned to a full commit SHA with an explicit `# vX.Y.Z` comment;
  `dependabot` bumps both the SHA and the comment (github-actions + gomod + docker).
- `make ci` is the gate: `gofmt`, `go vet`, `golangci-lint`, `go test -race`, `govulncheck`, build.
- A CycloneDX SBOM (`cyclonedx-gomod`) is produced as a CI artifact and a per-release asset.
- Releases run through GoReleaser (`CGO_ENABLED=0`, `-trimpath`, `mod_timestamp` for
  reproducibility). The container image is built multi-arch with SBOM + provenance attestations.
- Semgrep runs on every file write (local hook) and in CI; findings block. No inline
  `// nosemgrep` / `//nolint` suppressions — code is restructured instead (e.g. the
  `writeBytes(io.Writer, …)` test helper that sidesteps the write-to-ResponseWriter rule).

## Consequences
CI reproduces locally (everything is a Makefile target). Dependency drift is visible and
bounded. Unsigned macOS binaries strip the quarantine bit via the Homebrew cask post-install hook.
