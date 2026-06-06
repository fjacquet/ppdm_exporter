BIN := bin/ppdm_exporter
DIST := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Pinned tool versions (installed by `make tools`).
GOLANGCI_LINT_VERSION   ?= v2.12.2
CYCLONEDX_GOMOD_VERSION ?= latest
GOVULNCHECK_VERSION     ?= latest

.PHONY: tools tools-sbom cli report-cli test test-race test-coverage vet fmt fmt-check lint vuln sbom \
        sure ci release release-snapshot docker run-cli clean clean-dist demo demo-logs demo-down

# --- tooling ---

# Install pinned dev/CI tooling into $(GOBIN)/$GOPATH/bin.
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

# Just the SBOM generator — used by the release pipeline (GoReleaser sboms hook).
tools-sbom:
	go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)

# --- quality gates (used by CI) ---

fmt:
	gofmt -w .
fmt-check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)"
vet:
	go vet ./...
lint:
	golangci-lint run ./...
test:
	go test ./...
test-race:
	go test -race -cover ./...
test-coverage:
	go test -coverprofile=$(DIST)/coverage.out ./... && go tool cover -func=$(DIST)/coverage.out
vuln:
	govulncheck ./...

# Local convenience gate.
sure: fmt-check vet test cli
# Aggregate gate run by CI: fmt + vet + lint + race tests + vuln + build.
ci: fmt-check vet lint test-race vuln cli

# --- artifacts ---

cli:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

report-cli:
	go build -ldflags "$(LDFLAGS)" -o bin/report ./cmd/report

run-cli: cli
	./$(BIN) --config config.yaml --debug

docker:
	docker build -t ppdm_exporter:$(VERSION) .

# End-to-end demo stack: mockppdm -> exporter -> Prometheus -> Grafana, plus the backup
# reporter (postgres + cmd/report) feeding the SLA Compliance dashboard and the on-demand
# assurance report. Requires a running Docker daemon. Brings the stack up detached and prints
# where to look; stop it with `make demo-down`.
demo:
	docker compose up -d --build
	@echo ""
	@echo "Demo stack is up. Open:"
	@echo "  Grafana      http://localhost:3000  (admin/admin)"
	@echo "                 - 'PowerProtect Data Manager — Overview'  (live exporter metrics)"
	@echo "                 - 'PowerProtect — SLA Compliance'         (backup compliance verdicts)"
	@echo "  Report HTML  http://localhost:9103/report?tenant=acme-corp"
	@echo "  Report PDF   http://localhost:9103/report?tenant=acme-corp&format=pdf"
	@echo "  Mailpit      http://localhost:8025  (scheduled reports land here)"
	@echo "  Prometheus   http://localhost:9090"
	@echo ""
	@echo "The reporter captures every 30s — give it a moment after first start"
	@echo "(the report endpoint returns 404 until the first capture cycle completes)."
	@echo "Follow logs: make demo-logs   |   Stop: make demo-down"
demo-logs:
	docker compose logs -f
demo-down:
	docker compose down --remove-orphans

# CycloneDX SBOM for the Go module (source/dependency SBOM).
sbom:
	@mkdir -p $(DIST)
	cyclonedx-gomod mod -licenses -json -output $(DIST)/sbom.cdx.json
	@echo "wrote $(DIST)/sbom.cdx.json"

# Cross-compiled binaries + archives + SBOM + checksums + GitHub Release.
# Driven by GoReleaser (.goreleaser.yaml). Real releases run from a `v*` tag in CI;
# this target reproduces that pipeline locally — needs a tag and GITHUB_TOKEN.
release: tools-sbom
	goreleaser release --clean

# Local dry-run: full pipeline (build, archive, SBOM, checksums) without publishing.
release-snapshot: tools-sbom
	goreleaser release --snapshot --clean
	@echo "release artifacts in $(DIST)/"

clean-dist:
	rm -rf $(DIST)
clean: clean-dist
	rm -f $(BIN)
