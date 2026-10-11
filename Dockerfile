# syntax=docker/dockerfile:1
#
# Image for running meta-mangabaka standalone (as a remote plugin for meta).
#
# Until meta publishes tagged versions, go.mod points at a sibling meta
# checkout. The build gets it as a named build context:
#
#   docker build --build-context meta=../meta -t panelarr/meta-mangabaka .
#
# `make docker` does exactly that.

ARG GO_VERSION=1.27.2

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src/meta-mangabaka
# Only the parts of meta the plugin builds against: the generated protocol
# code and the SDK.
COPY --from=meta go.mod go.sum /src/meta/
COPY --from=meta gen /src/meta/gen
COPY --from=meta sdk /src/meta/sdk
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X github.com/panelarr/meta-mangabaka/internal/buildinfo.Version=${VERSION}" \
      -o /out/meta-mangabaka ./cmd/meta-mangabaka
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.title="meta-mangabaka" \
      org.opencontainers.image.description="MangaBaka metadata plugin for meta (Panelarr)" \
      org.opencontainers.image.source="https://github.com/panelarr/meta-mangabaka" \
      org.opencontainers.image.licenses="GPL-3.0-or-later" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/meta-mangabaka /meta-mangabaka
COPY --from=build --chown=65532:65532 /out/data /data
ENV MANGABAKA_DOWNLOAD_DIR=/data \
    PLUGIN_LISTEN=:8903
VOLUME ["/data"]
EXPOSE 8903
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/meta-mangabaka", "healthcheck"]
ENTRYPOINT ["/meta-mangabaka"]
