# syntax=docker/dockerfile:1
# `docker/dockerfile:1` — Recommended stable track; BuildKit will pull the latest minor/patch updates.

ARG GO_VERSION=1.25

# BuildKit automatically passes these args (for multi-arch builds).
ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM
ARG TARGETPLATFORM

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

# ---- Build metadata ----
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

# ---- Ports (it's better to have defaults so that EXPOSE doesn't break if args aren't passed) ----
ARG HTTP_PORT=8080
ARG GRPC_PORT=9090

# ---- Tool versions: Should be PINNING (not latest) for reproducibility ----
ARG PROTOC_GEN_GO_VERSION=latest
ARG PROTOC_GEN_GO_GRPC_VERSION=latest
ARG WIRE_VERSION=latest
ARG OAPI_CODEGEN_VERSION=latest

# Relocate GOPATH and caches to /go_workspace (outside /app) to avoid space issues AND go generate ./... collisions
ENV GOPATH=/go_workspace \
    GOMODCACHE=/go_workspace/pkg/mod \
    GOCACHE=/go_workspace/cache/go-build \
    GOTMPDIR=/go_workspace/tmp \
    PATH=$PATH:/go_workspace/bin \
    APP_VERSION=$APP_VERSION \
    GIT_COMMIT_HASH=$GIT_COMMIT_HASH \
    BUILD_TIME=$BUILD_TIME \
    CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH

# ---- System deps (protoc) ----
RUN apk add --no-cache protobuf-dev git

# Ensure workspace exists
RUN mkdir -p /go_workspace/pkg /go_workspace/cache /go_workspace/tmp

WORKDIR /app

# ---- Go build tools (cached) ----
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION} \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION} \
    && go install github.com/google/wire/cmd/wire@${WIRE_VERSION} \
    && go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${OAPI_CODEGEN_VERSION}

# ---- deps first (best cache hit rate) ----
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    go mod download

# ---- source ----
COPY cmd/ cmd/
COPY internal/ internal/
COPY configs/ configs/
COPY migrations/ migrations/
COPY api/ api/
COPY proto/ proto/
COPY sqlc.yaml ./

# ---- codegen ----
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    go generate ./...

# ---- build ----
RUN --mount=type=cache,target=/go_workspace/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go_workspace/cache/go-build,sharing=locked \
    go build \
    -buildvcs=false \
    -trimpath \
    -tags=go_json \
    -ldflags="-w -s \
    -X 'main.version=${APP_VERSION}' \
    -X 'main.commit=${GIT_COMMIT_HASH}' \
    -X 'main.buildTime=${BUILD_TIME}'" \
    -o /app/bin/paladin ./cmd/server

# ---- runtime ----
FROM --platform=$TARGETPLATFORM gcr.io/distroless/static:nonroot

ARG HTTP_PORT=8080
ARG GRPC_PORT=9090

WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /app/bin/paladin /app/bin/paladin
COPY --from=builder --chown=nonroot:nonroot /app/migrations /app/migrations
COPY --from=builder --chown=nonroot:nonroot /app/configs /app/configs

EXPOSE 8080
EXPOSE 9090

USER nonroot:nonroot
ENTRYPOINT ["/app/bin/paladin"]
