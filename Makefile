BIN  := bin/ppdm_exporter
DIST ?= dist
COVER ?= coverage.out
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Pinned tool versions (installed by `make tools`).
GOLANGCI_VERSION    ?= v2.12.2
GORELEASER_VERSION  ?= v2.18.0
CYCLONEDX_GOMOD_VERSION ?= latest

.PHONY: all clean install tools tools-sbom lint format fmt fmt-check vet test test-race \
        test-coverage build vuln sbom security docs coverage-upload release \
        release-snapshot ci sure \
        report-cli run-cli docker demo demo-logs demo-down

# --- canonical targets (fjacquet/ci standard) ---

all: clean lint test build

clean:
	rm -rf $(DIST) site $(COVER) *.sarif
	rm -f $(BIN)

install:
	go mod download

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# Just the SBOM generator — used by the release pipeline (GoReleaser sboms hook).
tools-sbom:
	go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)

format:
	golangci-lint fmt

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)"

vet:
	go vet ./...

lint:
	golangci-lint run --timeout=5m

test:
	go test -race -coverprofile=$(COVER) -covermode=atomic ./...

test-race:
	go test -race -cover ./...

test-coverage:
	go test -coverprofile=$(DIST)/coverage.out ./... && go tool cover -func=$(DIST)/coverage.out

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

sbom:
	@mkdir -p $(DIST)
	go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest mod -json -output $(DIST)/sbom.cdx.json
	@echo "wrote $(DIST)/sbom.cdx.json"

security:  # advisory: reports findings but never blocks the build (CodeQL/osv are the blocking gates)
	uvx semgrep scan --config auto --skip-unknown-extensions || true

docs:
	uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict --site-dir site

coverage-upload:
	uvx --from codecov-cli codecov upload-process --file $(COVER) || true

release: tools-sbom
	goreleaser release --clean

# Local dry-run: full pipeline (build, archive, SBOM, checksums) without publishing.
release-snapshot: tools-sbom
	goreleaser release --snapshot --clean
	@echo "release artifacts in $(DIST)/"

# Aggregate gate run by CI: lint + test -race + vuln + build.
ci: lint test build vuln

# --- repo-specific targets ---

report-cli:
	go build -ldflags "$(LDFLAGS)" -o bin/report ./cmd/report

run-cli: build
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
	@echo "  Report HTML  curl -H 'Authorization: Bearer demo-acme-token' 'http://localhost:9103/report?tenant=acme-corp'"
	@echo "  Report PDF   ...same with &format=pdf"
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

# Local convenience gate (kept for local dev).
sure: fmt-check vet test build
