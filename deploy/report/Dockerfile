# syntax=docker/dockerfile:1
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/report ./cmd/report

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/report /report
USER nonroot:nonroot
ENTRYPOINT ["/report"]
CMD ["--config", "/etc/report/config.report.yaml"]
