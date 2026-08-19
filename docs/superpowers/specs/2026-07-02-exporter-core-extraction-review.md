# Exporter Core Extraction — Design Review & Critique

**Status:** Draft Review  
**Author:** Principal Software Engineer & Code Review Architect  
**Date:** July 2, 2026  
**Target Design Spec:** `docs/superpowers/specs/2026-07-02-exporter-core-extraction-design.md`

---

## 1. Executive Summary

The proposed design to extract a vendor-neutral engine into a shared Go module (`fjacquet/exporter-core`) and centralize GitHub Actions workflows (`fjacquet/exporter-workflows`) is a highly mature and logical next step for managing Fred's family of 11 Go Prometheus + OTLP exporters. It addresses the operational pain points of repetitive feature rollouts, configuration drift, and CI upkeep across 11 repositories.

However, a first-principles architectural analysis reveals several **critical integration friction points** and **hidden maintenance traps** in the current proposal. Specifically, the design:
1. **Transfers operational friction** from "code copy-pasting" to "dependency/release coordination hell" across 12 separate repositories (1 core + 11 exporters).
2. **Over-abstracts configuration structures**, risking either breaking changes to vendor-specific config fields (like PPDM's `lookback` or `assetAgeThreshold`) or making the core configuration module too rigid.
3. **Risks making metric registration brittle** by shifting from a highly robust "dynamic/unchecked" model to a static schema `Catalog`, which could fail when vendor APIs return undocumented or modified metrics.
4. **Fails to define OpenTelemetry lifecycle management** regarding configuration reloads, potentially introducing severe metric-callback memory leaks.

This document provides a detailed critique of these areas and outlines **concrete technical architectures** (utilizing Go 1.18+ generics and hybrid registry patterns) to resolve these flaws before implementation begins.

---

## 2. Core Architectural Criticisms & Potential Pitfalls

### A. The Dependency Coordination Trap (Approach A vs. Approach B)
The decision to reject a monorepo (Approach B) is based on the fear of losing historical release lines, GitHub container registry configurations, and Pages deployments. While this keeps repository separation clean, it introduces a major **dependency bottleneck**:
* **The Coordination Pain:** Every time a bug is fixed or a feature is added in `fjacquet/exporter-core`, you must:
  1. Cut a release for `fjacquet/exporter-core` (e.g., `v1.2.3`).
  2. Run 11 individual PR pipelines across all 11 repositories to update `go.mod` to pin `v1.2.3`.
  3. Approve and merge 11 PRs.
  4. Trigger and verify 11 separate GoReleaser pipelines to push new Docker images and Homebrew casks.
* **Critique:** Instead of eliminating feature rollout overhead, this approach converts **copy-paste friction** into **coordination friction**.
* **Mitigation Recommendation:** 
  * If a monorepo is rejected, you **must automate downstream updates**. Implement a repository dispatch workflow or a central update script in `exporter-workflows` that can programmatically run `go get github.com/fjacquet/exporter-core@vX.Y.Z && go mod tidy` and create PRs across all 11 repos simultaneously.
  * *Alternative Consideration:* A multi-module monorepo using Go workspaces. Git subtree or `git-filter-repo` can merge the history of all 11 repositories into a single monorepo while retaining full commit histories. From a single monorepo, a unified GitHub Action can multiplex GoReleaser and publish to individual GHCR tags/casks with 90% less maintenance overhead.

---

### B. Configuration Over-Abstraction & Extensibility
The design states that `exporter-core/config` will own `LoadYAML`, `${ENV}` expansion, dotenv, SIGHUP, and fsnotify validated cancelable hot reloading. 
* **The Flaw:** In `ppdm_exporter`, the `Collection` struct includes highly specific fields like `Lookback`, `AssetAgeThreshold`, and `PerJobActivities`. Other exporters (e.g., `pve_exporter` or `idrac_exporter`) will have completely different, non-overlapping configuration fields (e.g., `exclude_nodes`, `snmp_retries`, etc.). If `exporter-core` defines a rigid `Config` struct, it cannot be generalized. If it is not generalized, you will end up with dead fields in other exporters or a complex `interface{}` schema that lacks type-safety and automatic yaml validation.
* **The Solution — Generic Config Wrapper:** `exporter-core` should define a generic configuration parser using Go generics. The core defines the *shared infrastructure settings* (such as HTTP server, common scraping timers, and OTel endpoints), but delegates the target-server definition and custom metrics options to a parameterized type `T`.

#### Concrete Go Generic Implementation Recommendation:
```go
package config

import (
	"time"
	"gopkg.in/yaml.v3"
)

// SharedConfig represents parameters required by every single exporter engine.
type SharedConfig struct {
	Server     ServerHTTP `yaml:"server"`
	Collection Collection `yaml:"collection"`
	OTel       OTel       `yaml:"otel"`
}

type ServerHTTP struct {
	Host    string `yaml:"host"`
	Port    string `yaml:"port"`
	URI     string `yaml:"uri"`
	LogName string `yaml:"logName"`
}

type Collection struct {
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
}

type OTel struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
	Interval string `yaml:"interval"`
}

// Config wraps the core shared settings and allows the specific exporter
// to inject its own custom target/server arrays and parameters (CustomT).
type Config[CustomT any] struct {
	SharedConfig `yaml:",inline"`
	Custom       CustomT `yaml:",inline"`
}

// Watcher manages file changes and emits fully-typed, re-validated configs.
type Watcher[CustomT any] struct {
	path    string
	updates chan *Config[CustomT]
	// ... (retaining the robust fsnotify + SIGHUP directory watch logic)
}
```
This generic-based design preserves complete structural integrity, compile-time type safety, and automatic YAML unmarshaling for custom fields in each exporter, while keeping the core watcher engine 100% vendor-neutral.

---

### C. Telemetry Catalog Brittleness vs. Unchecked Dynamics
The design proposes introducing a `Catalog` constructed by the exporter and passed to the core to map help text, OTLP metric names, and ordered label-key sets.
* **The Risk:** One of the most resilient architectural elements of `ppdm_exporter` is its **dynamic/unchecked** design:
  - `PromCollector` describes nothing and dynamically converts snapshot samples into gauges. If a label set is inconsistent, it skips it instead of panicking.
  - `OTLPExporter` registers instruments on-the-fly dynamically when it spots a new metric name in a snapshot.
* **Critique:** If a strict, hand-written `Catalog` is introduced, any metric returned by a vendor API update that is missing from the `Catalog` might be silently dropped or cause validation failures. Writing and maintaining a schema map of hundreds of metrics per exporter by hand introduces significant boilerplate, directly violating Pain Point 3.
* **The Solution — Hybrid Dynamic Catalog:** The `Catalog` should not be a rigid, blocking registry. It must act as an *advisory metadata provider*. If a metric is looked up in the `Catalog` and missing, the core engine must fall back to the existing dynamic behavior (e.g., auto-generating a default help text `"PPDM metric " + s.Name` and auto-generating labels from the slice).

#### Concrete Interface Design:
```go
package snapshot

type MetricMetadata struct {
	Help            string
	OTLPMetricName  string
	OrderedLabelKeys []string
	Unit            string
}

// Catalog acts as an optional descriptor directory.
type Catalog interface {
	// Lookup seeks metadata for a given metric. If present, returns (metadata, true).
	// If absent, returns (fallbackMetadata, false) to preserve unchecked dynamic emission.
	Lookup(metricName string) (MetricMetadata, bool)
}
```

---

### D. OpenTelemetry Lifecycle & Memory Leak Hazard
In `otlp.go`, the OTLP exporter registers asynchronous observable gauges via callbacks (e.g., `Float64ObservableGauge`).
* **The Problem:** In the OpenTelemetry Go SDK, callbacks registered to a `Meter` are long-lived and difficult to cleanly unregister.
* **The Reload Hazard:** When a configuration reload occurs (due to fsnotify or SIGHUP), the exporter will likely re-initialize the `MeterProvider` or reload the targets.
  - If the core simply spins up a new `MeterProvider` or adds new instruments to an existing `Meter` without shutting down the old one, callbacks holding references to old `SnapshotStore` instances will accumulate in memory, causing a severe **memory leak** and **CPU thread thrashing** as old callbacks continue to run.
* **The Solution:** The `exporter-core/export` package must explicitly manage the lifecycle of the `MeterProvider`. Upon a validated configuration update, the core must cleanly call `Shutdown(ctx)` on the old `MeterProvider` before instantiating the new one, ensuring all registered callbacks are released by the runtime.

---

### E. Pilot Exporter Selection & Report Decoupling
The choice of `ppdd_exporter` (Data Domain) as the pilot is excellent. It is clean, lacks the reporting engine, and uses the newer `config + client` layout.
* **The Danger with PPDM (Second in queue):** `ppdm_exporter` is much more complex because it contains a PostgreSQL-backed reporting daemon (`cmd/report` and `internal/report`) which currently shares `internal/config`.
* **Critique:** During the extraction of `exporter-core`, we must be extremely vigilant not to pull database migrations, cron schedules, or SLA/delivery schemas into the core. 
* **Recommendation:** The core should remain completely devoid of database/SQL components. The PPDM reporting logic must be kept entirely in the `ppdm_exporter` local repo, treating `exporter-core` purely as its telemetry collector dependency.

---

## 3. Workflow Centralization & Local Testing friction
Moving `ci.yml`, `release.yml`, and `docs.yml` into a central, reusable workflow repository (`fjacquet/exporter-workflows`) reduces maintenance churn significantly. However, it introduces another risk:
* **CI Blind Spot:** Centralized actions pinned via `@v1` tags make it incredibly difficult for individual exporters to test workflow modifications locally (e.g., testing new linter rules or Go compilation flags) without cutting a release on the workflow repository.
* **Recommendation:** Always maintain a standard local-testing strategy using local Makefiles or tooling (like `act`). The reusable central workflow files should be thin wrappers over standard shell scripts/Make targets (`make lint`, `make test`, `make build`) maintained inside each repository, rather than having the GitHub Actions run massive, un-testable custom workflow blocks.

---

## 4. Final Recommendations & Roadmap Adjustments

To ensure the success of this major architectural refactor, I recommend updating the design document with the following amendments before coding begins:

1. **Adopt Go Generics for Configuration:** Implement `Config[CustomT]` and `Watcher[CustomT]` in `exporter-core/config` to allow individual exporters to cleanly define and validate their own target server types and lookback fields without breaking core engine sharing.
2. **Implement Hybrid Catalog Resolution:** Ensure that the Prometheus unchecked collector and OTel instruments default to auto-discovery when a metric is missing from the compiled `Catalog`. Do not make the catalog a blocking requirement.
3. **Explicitly Detail the OTel Lifecycle:** Add a dedicated lifecycle section to the design confirming that `MeterProvider.Shutdown(ctx)` is invoked during config reloads to purge stale observable gauge callbacks.
4. **Develop Workflow Downstream PR Automation:** Create a composite action or central repository dispatch workflow in `exporter-workflows` to handle the fan-out of dependency updates across the other 10 repositories to mitigate the coordination tax.
