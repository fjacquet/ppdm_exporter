# syntax=docker/dockerfile:1
# Build context is the repo root: docker build -f deploy/mockppdm/Dockerfile .
FROM golang:1.26.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mockppdm ./cmd/mockppdm

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mockppdm /mockppdm
USER nonroot:nonroot
EXPOSE 8443
ENTRYPOINT ["/mockppdm"]
