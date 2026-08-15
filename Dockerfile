# syntax=docker/dockerfile:1
FROM docker.io/library/golang:1.26.6 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/ppdm_exporter .

FROM docker.io/library/alpine:latest

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
# (see ADR-0015).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://127.0.0.1:9442/livez || exit 1

USER ppdm

ENTRYPOINT ["/usr/bin/ppdm_exporter"]
CMD ["--config", "/etc/ppdm_exporter/config.yaml"]
