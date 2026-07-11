# syntax=docker/dockerfile:1
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/ppdm_exporter .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/ppdm_exporter /ppdm_exporter
USER nonroot:nonroot
EXPOSE 9442
ENTRYPOINT ["/ppdm_exporter"]
CMD ["--config", "/etc/ppdm_exporter/config.yaml"]
