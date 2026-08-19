# syntax=docker/dockerfile:1
#
# Paladin container image — three stages:
#
#   1. tools   — installs codegen binaries (buf, sqlc, protoc-gen-*)
#                on BUILDPLATFORM. Pinned versions + cache mounts.
#   2. builder — runs codegen + go build for TARGETPLATFORM.
#   3. runtime — distroless static; ENTRYPOINTs the binary.
#
# Migrations are embedded into the binary (migrations/migrations.go); the
# image carries no on-disk SQL. Configs are mounted at runtime via the
# Helm chart's ConfigMap or the docker-compose volume bind, never baked in.

# GO_VERSION is the human-readable tag; the @sha256 digest on the FROM is the
# authoritative pin (a tag is mutable, a digest is not). Renovate keeps both
# in lockstep — see renovate.json. Bump the tag and digest together.
ARG GO_VERSION=1.26

# BuildKit auto-fills these for multi-arch builds.
ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM

# -----------------------------------------------------------------------------
# Shared Go base — same toolchain + workspace layout for tools and builder.
# Everything below extends this stage so we don't duplicate ENV blocks.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS gobase

ENV GOPATH=/go_workspace \
    GOMODCACHE=/go_workspace/pkg/mod \
    GOCACHE=/go_workspace/cache/go-build \
    GOTMPDIR=/go_workspace/tmp \
    GOBIN=/go_workspace/bin \
    PATH=/go_workspace/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    CGO_ENABLED=0

# `git` is a hard dep for `go install` (resolves module proxy fallbacks).
# `mkdir -p` is required for GOTMPDIR + GOBIN — Go stat-checks GOTMPDIR
# before use and refuses to start with `creating work dir: stat …: no
# such file or directory`. GOMODCACHE + GOCACHE are mounted at use-site
# via BuildKit cache mounts, so they don't need pre-creation.
RUN apk add --no-cache git && \
    mkdir -p /go_workspace/tmp /go_workspace/bin

# -----------------------------------------------------------------------------
# Tools stage — code-generation binaries built for BUILDPLATFORM. Pinned
# versions are required for reproducible images; cache mounts make repeat
# builds fast.
# -----------------------------------------------------------------------------
FROM gobase AS tools

ARG PROTOC_GEN_GO_VERSION=v1.36.11
ARG PROTOC_GEN_CONNECT_GO_VERSION=v1.19.1
ARG SQLC_VERSION=v1.30.0
ARG BUF_VERSION=v1.50.0

RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION} && \
    go install connectrpc.com/connect/cmd/protoc-gen-connect-go@${PROTOC_GEN_CONNECT_GO_VERSION} && \
    go install github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION} && \
    go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}

# -----------------------------------------------------------------------------
# Builder stage — runs codegen + cross-compiles the binary.
# -----------------------------------------------------------------------------
FROM gobase AS builder

ARG TARGETOS
ARG TARGETARCH

# Build metadata threaded into the binary's main package via -ldflags.
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

# protoc is the only extra apk dep beyond gobase — buf shells out to it.
RUN apk add --no-cache protobuf

COPY --from=tools /go_workspace/bin/ /go_workspace/bin/

WORKDIR /app

# Module files first — best cache hit on dependency-only changes.
# Build context is the REPOSITORY ROOT, not backend/ — the binary depends on
# the nested capability module at repo-root capability/, which a backend-only
# context cannot reach (`go mod download` fails resolving the relative
# replace). Every path below is therefore repo-relative.
COPY --link backend/go.mod backend/go.sum ./
# The nested module must land BEFORE `go mod download`, because the replace
# directive in backend/go.mod resolves against it.
COPY --link capability/ /capability/
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    go mod download -x

# Source layout. configs/ is intentionally NOT copied — runtime configs come
# from a Helm ConfigMap (k8s) or a host bind mount (compose), never baked in.
COPY --link backend/cmd/ cmd/
COPY --link backend/internal/ internal/
COPY --link backend/migrations/ migrations/
# proto/ is a self-contained buf module — buf.yaml/buf.gen.yaml/buf.lock now
# live inside it (copied by the line above), so only sqlc.yaml is copied here.
COPY --link backend/proto/ proto/
COPY --link backend/sqlc.yaml ./

RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    buf generate proto --template proto/buf.gen.yaml && \
    go generate ./...

RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
        -buildvcs=false \
        -trimpath \
        -tags=go_json \
        -ldflags="-w -s \
            -X 'main.version=${APP_VERSION}' \
            -X 'main.commit=${GIT_COMMIT_HASH}' \
            -X 'main.buildTime=${BUILD_TIME}'" \
        -o /out/paladin-core ./cmd/server

# -----------------------------------------------------------------------------
# Runtime stage — distroless static. No shell, no curl, no package manager.
# Health checks are kubelet-side (httpGet probes); compose drops its
# in-container healthcheck for the same reason.
# -----------------------------------------------------------------------------
# Digest-pinned (Renovate-managed, see renovate.json). `:nonroot` is a
# floating tag that moves often, so the digest is the real reproducibility pin.
FROM gcr.io/distroless/static:nonroot@sha256:963fa6c544fe5ce420f1f54fb88b6fb01479f054c8056d0f74cc2c6000df5240

ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

LABEL org.opencontainers.image.title="paladin-core" \
      org.opencontainers.image.description="Paladin — Connect RPC service" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT_HASH}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="https://github.com/oleg-tkachuk/paladin"

WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /out/paladin-core /app/bin/paladin-core

# Three-plane HTTP service. Documentation only — Kubernetes binds via the
# Service object, compose binds via the `ports:` stanza.
#   8080 — data plane (object operations)
#   8085 — iam plane (login + token exchange)
#   8090 — admin plane (internal/SRE)
EXPOSE 8080 8085 8090

USER nonroot:nonroot
ENTRYPOINT ["/app/bin/paladin-core"]
