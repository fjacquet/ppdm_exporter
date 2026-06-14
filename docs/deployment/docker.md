# Docker

```bash
docker run --rm -p 9442:9442 \
  -v "$PWD/config.yaml:/etc/ppdm_exporter/config.yaml:ro" \
  -e PPDM1_PASSWORD \
  ghcr.io/fjacquet/ppdm_exporter:latest
```

The image:

- is multi-arch (`linux/amd64`, `linux/arm64`),
- is built `FROM gcr.io/distroless/static:nonroot` and runs as **non-root**,
- ships with SBOM + provenance attestations (verify with `docker buildx imagetools inspect`),
- defaults to `--config /etc/ppdm_exporter/config.yaml` — mount yours there.

Build locally:

```bash
make docker      # docker build -t ppdm_exporter:<version> .
```

Pass secrets as environment variables (referenced via `${ENV}` in the config) or mount a
`passwordFile`. Never bake credentials into the image.
