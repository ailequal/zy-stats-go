# syntax=docker/dockerfile:1

# Build: cross-compile the static Go binary on the build platform.
# =================================================================
# golang:1.27-alpine (Go 1.27.2), multi-arch index digest pinned on 2026-10-11.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build

WORKDIR /src
ENV CGO_ENABLED=0

# Dependencies first, so the module download layer is reused until go.mod or
# go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd/ cmd/
COPY internal/ internal/

# TARGETOS/TARGETARCH come from buildx; VERSION and BUILD_DATE from the build
# command (`just docker-build` passes `git describe` and the commit date).
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG BUILD_DATE=
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildDate=${BUILD_DATE}" -o /out/zy-stats-go ./cmd/zy-stats-go

# Production: static binary on distroless, non-root, no shell.
# ============================================================
# gcr.io/distroless/static-debian13:nonroot ships ca-certificates, tzdata and
# the nonroot user (uid 65532); multi-arch index digest pinned on 2026-10-11.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99 AS prod

ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=
LABEL org.opencontainers.image.title="zy-stats-go" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.description="Fetch the Zyxel Nebula FWA505 signal stats from the CLI" \
      org.opencontainers.image.source="https://github.com/ailequal/zy-stats-go" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/zy-stats-go /usr/local/bin/zy-stats-go

# /app holds the mounted .env and the logs directory (./logs:/app/logs in
# compose.prod.yaml). Log files roll over at midnight in the container's TZ.
WORKDIR /app
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/zy-stats-go"]
