# syntax=docker/dockerfile:1.7
# Multi-stage build: small distroless image, cached module + build layers.

FROM golang:1.26.6 AS build
WORKDIR /src
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/hoomlab ./cmd/hoomlab

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hoomlab /usr/local/bin/hoomlab
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/hoomlab"]
