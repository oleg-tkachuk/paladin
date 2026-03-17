# syntax=docker/dockerfile:1
# `docker/dockerfile:1` — Recommended stable track; BuildKit will pull the latest minor/patch updates.

ARG GO_VERSION=1.25

# BuildKit automatically passes these args (for multi-arch builds).
ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM
ARG TARGETPLATFORM

# -----------------------------
# Tools stage (build platform)
# -----------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS tools

# Tool versions MUST be pinned for reproducible builds
ARG PROTOC_GEN_GO_VERSION=latest
ARG PROTOC_GEN_GO_GRPC_VERSION=latest
ARG WIRE_VERSION=latest
ARG PROTOC_GEN_CONNECT_GO_VERSION=latest
ARG SQLC_VERSION=latest
ARG BUF_VERSION=latest

ENV GOPATH=/go_workspace \
    GOMODCACHE=/go_workspace/pkg/mod \
    GOCACHE=/go_workspace/cache/go-build \
    GOTMPDIR=/go_workspace/tmp \
    GOBIN=/go_workspace/bin \
    PATH=/go_workspace/bin:$PATH

RUN apk add --no-cache git

RUN mkdir -p /go_workspace/pkg /go_workspace/cache /go_workspace/tmp /go_workspace/bin

# Install Go build tools for the BUILDPLATFORM (do NOT set GOOS/GOARCH here)
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION} && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION} && \
    go install github.com/google/wire/cmd/wire@${WIRE_VERSION} && \
    go install connectrpc.com/connect/cmd/protoc-gen-connect-go@${PROTOC_GEN_CONNECT_GO_VERSION} && \
    go install github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION} && \
    go install github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}

# -----------------------------
# Builder stage (target binary)
# -----------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

# Build metadata
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

# Runtime ports (documentation only)
ARG HTTP_PORT=8080
ARG GRPC_PORT=9090

ENV GOPATH=/go_workspace \
    GOMODCACHE=/go_workspace/pkg/mod \
    GOCACHE=/go_workspace/cache/go-build \
    GOTMPDIR=/go_workspace/tmp \
    PATH=/go_workspace/bin:$PATH \
    CGO_ENABLED=0

# protoc is needed for .proto codegen; git might be needed by generators
RUN apk add --no-cache protobuf git

RUN mkdir -p /go_workspace/pkg /go_workspace/cache /go_workspace/tmp /go_workspace/bin

# Bring tools from tools stage
COPY --from=tools /go_workspace/bin/ /go_workspace/bin/

WORKDIR /app

# Copy module files first for better cache hits
COPY --link go.mod go.sum ./

RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    go mod download -x

# Copy source
COPY --link cmd/ cmd/
COPY --link internal/ internal/
COPY --link configs/ configs/
COPY --link migrations/ migrations/
COPY --link proto/ proto/
COPY --link buf.yaml buf.gen.yaml buf.lock ./
COPY --link sqlc.yaml ./

# Code generation
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    buf generate && \
    go generate ./...

# Build (set GOOS/GOARCH only here)
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
    -o /out/paladin ./cmd/server

# -----------------------------
# Runtime stage
# -----------------------------
FROM --platform=$TARGETPLATFORM gcr.io/distroless/static:nonroot

ARG HTTP_PORT=8080
ARG GRPC_PORT=9090
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

# OCI image labels
LABEL org.opencontainers.image.title="paladin" \
    org.opencontainers.image.description="acme PALADIN API — Connect RPC + gRPC service" \
    org.opencontainers.image.version="${APP_VERSION}" \
    org.opencontainers.image.revision="${GIT_COMMIT_HASH}" \
    org.opencontainers.image.created="${BUILD_TIME}" \
    org.opencontainers.image.source="https://github.com/oleg-tkachuk/paladin"

WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /out/paladin /app/bin/paladin
COPY --from=builder --chown=nonroot:nonroot /app/migrations /app/migrations
COPY --from=builder --chown=nonroot:nonroot /app/configs /app/configs

# EXPOSE is documentation; you may keep constants or use args
EXPOSE 8080 9090

USER nonroot:nonroot
ENTRYPOINT ["/app/bin/paladin"]
