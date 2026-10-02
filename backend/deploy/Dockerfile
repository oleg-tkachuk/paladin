# syntax=docker/dockerfile:1
#
# Paladin container image — two stages:
#
#   1. builder — go build for TARGETPLATFORM from the committed sources.
#   2. runtime — distroless static; ENTRYPOINTs the binary.
#
# Migrations are embedded into the binary (migrations/migrations.go); the
# image carries no on-disk SQL. Configs are mounted at runtime via the
# Helm chart's ConfigMap or the docker-compose volume bind, never baked in.

# GO_VERSION is the human-readable tag; the @sha256 digest on the FROM is the
# authoritative pin (a tag is mutable, a digest is not). Renovate keeps both
# in lockstep — see renovate.json. Bump the tag and digest together.
ARG GO_VERSION=1.27

# BuildKit auto-fills these for multi-arch builds.
ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM
ARG TARGETPLATFORM

# -----------------------------------------------------------------------------
# Go base — the toolchain and workspace layout the builder extends.
# Everything below extends this stage so we don't duplicate ENV blocks.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS gobase

ENV GOPATH=/go_workspace \
    GOMODCACHE=/go_workspace/pkg/mod \
    GOCACHE=/go_workspace/cache/go-build \
    GOTMPDIR=/go_workspace/tmp \
    GOBIN=/go_workspace/bin \
    PATH=/go_workspace/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    CGO_ENABLED=0

# No git: every module comes through the module proxy, and the two sibling
# modules through replace directives. `mkdir -p` is required for GOTMPDIR +
# GOBIN — Go stat-checks GOTMPDIR before use and refuses to start with
# `creating work dir: stat …: no such file or directory`. GOMODCACHE + GOCACHE
# are mounted at use-site via BuildKit cache mounts, so they don't need
# pre-creation.
RUN mkdir -p /go_workspace/tmp /go_workspace/bin

# -----------------------------------------------------------------------------
# Builder stage — cross-compiles the binary from the committed sources.
# -----------------------------------------------------------------------------
FROM gobase AS builder

ARG TARGETOS
ARG TARGETARCH

# Build metadata threaded into the binary's main package via -ldflags, each
# only when given: an unset one keeps the default cmd/server/root.go declares
# rather than baking a placeholder the console then displays.
ARG APP_VERSION
ARG GIT_COMMIT_HASH
ARG BUILD_TIME

WORKDIR /app

# Module files first — best cache hit on dependency-only changes.
# Build context is the REPOSITORY ROOT, not backend/ — the binary depends on
# the nested capability module at repo-root capability/, which a backend-only
# context cannot reach (`go mod download` fails resolving the relative
# replace). Every path below is therefore repo-relative.
COPY --link backend/go.mod backend/go.sum ./
# The sibling modules must land BEFORE `go mod download`, because the replace
# directives in backend/go.mod resolve against them. sdk/go carries the
# committed protobuf and Connect stubs; they are not regenerated here.
COPY --link capability/ /capability/
COPY --link sdk/go/ /sdk/go/
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    go mod download -x

# Source layout. configs/ is intentionally NOT copied — runtime configs come
# from a Helm ConfigMap (k8s) or a host bind mount (compose), never baked in.
COPY --link backend/cmd/ cmd/
COPY --link backend/internal/ internal/
COPY --link backend/migrations/ migrations/
# policies/ embeds the Cedar schema and the default tenant policy (go:embed).
COPY --link backend/policies/ policies/

# No `go generate`: the sqlc bindings and mocks are committed, and the gates
# fail when they drift from their sources — the image builds what was reviewed.
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
        -buildvcs=false \
        -trimpath \
        -tags=go_json \
        -ldflags="-w -s \
            ${APP_VERSION:+-X main.version=${APP_VERSION}} \
            ${GIT_COMMIT_HASH:+-X main.commit=${GIT_COMMIT_HASH}} \
            ${BUILD_TIME:+-X main.buildTime=${BUILD_TIME}}" \
        -o /out/paladin-core ./cmd/server

# -----------------------------------------------------------------------------
# CI cache hand-off — never part of the image. A fresh CI runner starts with
# an empty build cache, so the builder's `go build` ran cold every time.
# scripts/go-build-cache.sh builds cache-import to fill the build cache mount
# from an archive actions/cache restored, and cache-export to write it out as
# one archive for the next run: one archive, because moving the cache file by
# file through BuildKit is slower than the build it saves. Only the build cache
# travels — the module cache is larger than the download it would save. The
# mount target must match the builder's exactly, because BuildKit keys a cache
# mount by its target. These stages sit before runtime, which stays the
# default target.
# -----------------------------------------------------------------------------
FROM gobase AS cache-import
# Changes every run, so BuildKit never skips the copy as already done.
ARG CACHE_RUN
# The build context holds only the archive cache-export wrote. Its go-build/
# lands on the cache mount, which sits at that path.
RUN --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    --mount=type=bind,source=cache.tar,target=/tmp/cache.tar \
    echo "cache import ${CACHE_RUN}" && \
    tar -xf /tmp/cache.tar -C /go_workspace/cache

FROM gobase AS cache-collect
ARG CACHE_RUN
RUN --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    echo "cache export ${CACHE_RUN}" && \
    mkdir -p /export/go-build && \
    cp -a /go_workspace/cache/go-build/. /export/go-build/

FROM scratch AS cache-export
COPY --from=cache-collect /export /

# -----------------------------------------------------------------------------
# Runtime stage — distroless static. No shell, no curl, no package manager.
# Health checks are kubelet-side (httpGet probes); compose drops its
# in-container healthcheck for the same reason.
# -----------------------------------------------------------------------------
# Digest-pinned (Renovate-managed, see renovate.json). `:nonroot` is a
# floating tag that moves often, so the digest is the real reproducibility pin.
FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7 AS runtime

ARG APP_VERSION
ARG GIT_COMMIT_HASH
ARG BUILD_TIME
# The distroless `nonroot` user. Numeric, so a runtime that cannot read the
# image's /etc/passwd — and a PodSecurity check for runAsNonRoot — still sees
# a non-root UID.
ARG APP_UID=65532
ARG APP_GID=65532

LABEL org.opencontainers.image.title="paladin-core" \
      org.opencontainers.image.description="Paladin — Connect RPC service" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT_HASH}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="https://github.com/oleg-tkachuk/paladin"

WORKDIR /app

COPY --from=builder --chown=${APP_UID}:${APP_GID} /out/paladin-core /app/bin/paladin-core

# Three-plane HTTP service. Documentation only — Kubernetes binds via the
# Service object, compose binds via the `ports:` stanza.
#   8080 — data plane (object operations)
#   8085 — iam plane (login + token exchange)
#   8090 — admin plane (internal/SRE)
EXPOSE 8080 8085 8090

USER ${APP_UID}:${APP_GID}

# No image-level healthcheck: the image is distroless (nothing to run one
# with), and what "healthy" means depends on the role — /livez on a plane,
# /healthz on the worker. The chart's probes and compose's service checks are
# per role.
HEALTHCHECK NONE

ENTRYPOINT ["/app/bin/paladin-core"]
