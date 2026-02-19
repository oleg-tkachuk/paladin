# syntax=docker/dockerfile:1
# `docker/dockerfile:1` — Recommended stable track; BuildKit will pull the latest minor/patch updates.  [oai_citation:0‡Docker Documentation](https://docs.docker.com/build/buildkit/frontend/?utm_source=chatgpt.com)

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

ENV APP_VERSION=$APP_VERSION \
    GIT_COMMIT_HASH=$GIT_COMMIT_HASH \
    BUILD_TIME=$BUILD_TIME \
    CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    GOMODCACHE=/go/pkg/mod \
    GOCACHE=/root/.cache/go-build

# ---- System deps (protoc) ----
RUN apk add --no-cache protobuf-dev git

# ---- Go build tools (cached) ----
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION} \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION} \
    && go install github.com/google/wire/cmd/wire@${WIRE_VERSION} \
    && go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${OAPI_CODEGEN_VERSION}

WORKDIR /app

# ---- deps first (best cache hit rate) ----
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
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
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go generate ./...

# ---- build ----
# IMPORTANT: remove `-a` and `-installsuffix cgo` — they break the incremental cache and slow down the build unnecessarily when CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
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
# Distroless: Minimal attack surface (no shell/package manager).  [oai_citation:1‡GitHub](https://github.com/GoogleContainerTools/distroless?utm_source=chatgpt.com)
# For multi-arch, note that historically `gcr.io/distroless/static:nonroot` had nuances with arch.  [oai_citation:2‡GitHub](https://github.com/GoogleContainerTools/distroless/issues/583?utm_source=chatgpt.com)
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

