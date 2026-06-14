# Installation

## From source

Requires Go 1.26.4+.

```bash
git clone https://github.com/fjacquet/ppdm_exporter
cd ppdm_exporter
make cli          # builds bin/ppdm_exporter
```

## Container image

```bash
docker pull ghcr.io/fjacquet/ppdm_exporter:latest
docker run --rm -p 9442:9442 \
  -v "$PWD/config.yaml:/etc/ppdm_exporter/config.yaml:ro" \
  -e PPDM1_PASSWORD \
  ghcr.io/fjacquet/ppdm_exporter:latest
```

The image is multi-arch (`linux/amd64`, `linux/arm64`), distroless, and runs as a non-root user.

## Homebrew (macOS)

```bash
brew install --cask fjacquet/tap/ppdm_exporter
```

## Verify

```bash
./bin/ppdm_exporter --version
```
