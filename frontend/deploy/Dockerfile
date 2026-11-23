# syntax=docker/dockerfile:1@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183
#
# Paladin console image (ADR-0049) — four stages:
#
#   1. deps    — installs pnpm dependencies from a clean lockfile on
#                BUILDPLATFORM. Cached when package.json / lockfile
#                don't change.
#   2. builder — `next build` with output: "export": the console as static
#                files, no server code.
#   3. server  — builds the Go server with the export embedded.
#   4. runtime — distroless static with the one binary; no Node.
#
# Build metadata (APP_VERSION / GIT_COMMIT_HASH / BUILD_TIME) is threaded
# through as build-args from Taskfile's metadata:write step; Next inlines it
# into the bundle.
#
# Package manager: pnpm. Version pinned to the `packageManager` field
# in package.json (Corepack reads it).

# NODE_VERSION and GO_VERSION are the human-readable tags; the @sha256 digest
# on each FROM is the authoritative pin (a tag is mutable, a digest is not).
# Renovate keeps both in lockstep — see renovate.json. Bump tag and digest
# together; the Go pair matches backend/deploy/Dockerfile.
ARG NODE_VERSION=26
# renovate: datasource=npm depName=pnpm
ARG PNPM_VERSION=12.22.0
ARG GO_VERSION=1.27

# -----------------------------------------------------------------------------
# Stage 1 — deps. Pinned to BUILDPLATFORM so a multi-arch run doesn't
# re-fetch the pnpm content-addressable store per arch (the resulting
# node_modules are pure JS).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS deps
ARG PNPM_VERSION

# `node:${VERSION}-alpine` ships npm but NOT corepack since upstream removed
# it from official images — install pnpm directly to match the local-dev pin
# in package.json's `packageManager` field. No libc6-compat: the native
# addons in the tree ship musl builds.
RUN npm install -g pnpm@${PNPM_VERSION}
WORKDIR /app

# Copy lockfile + manifest first so this layer caches across source-
# only changes. pnpm-workspace.yaml holds the `allowBuilds` security
# gate (see frontend/pnpm-workspace.yaml).
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./

# `--frozen-lockfile` is the CI-safe install: fails fast if
# pnpm-lock.yaml is out of date instead of silently re-resolving.
RUN pnpm install --frozen-lockfile

# -----------------------------------------------------------------------------
# Stage 2 — builder. `next build` writes the static export to out/, with
# build-time metadata baked in via NEXT_PUBLIC_-prefixed env vars.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS builder
ARG PNPM_VERSION

WORKDIR /app
RUN npm install -g pnpm@${PNPM_VERSION}
COPY --from=deps /app/node_modules ./node_modules
COPY . .

# Build-time metadata. `undefined` defaults keep the image buildable
# from `docker build .` without Taskfile threading the values through.
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

ENV NEXT_TELEMETRY_DISABLED=1 \
    APP_VERSION=${APP_VERSION} \
    GIT_COMMIT_HASH=${GIT_COMMIT_HASH} \
    BUILD_TIME=${BUILD_TIME}

RUN pnpm run build

# -----------------------------------------------------------------------------
# Stage 3 — server. The Go server with the export embedded (server/dist),
# cross-compiled for TARGETPLATFORM.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS server
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOMODCACHE=/go/pkg/mod \
    GOCACHE=/go/cache/go-build

WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download

COPY server/ ./
# go:embed takes what is in server/dist/out when the binary compiles.
COPY --from=builder /app/out/ ./dist/out/

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/go/cache/go-build,sharing=locked \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -buildvcs=false -trimpath -ldflags="-w -s" \
        -o /out/paladin-console ./cmd/paladin-console

# -----------------------------------------------------------------------------
# Stage 4 — runtime. One static binary on distroless.
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5 AS runtime

# ARGs do not cross stage boundaries — redeclared for the labels.
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined
# The app user. Numeric in USER, so a runtime that cannot read the image's
# /etc/passwd — and a PodSecurity check for runAsNonRoot — still sees a
# non-root UID. Must equal the chart's runAsUser / runAsGroup.
ARG APP_UID=10001
ARG APP_GID=10001
# The port the server listens on (its default); also what the healthcheck dials.
ARG APP_PORT=3000

# Which commit is this image? `task verify-e2e` prints these before it starts,
# so a Playwright run is always attributable to a console build.
LABEL org.opencontainers.image.title="paladin-console" \
      org.opencontainers.image.description="Paladin — admin console" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT_HASH}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.source="https://github.com/oleg-tkachuk/paladin"

COPY --from=server --chown=${APP_UID}:${APP_GID} /out/paladin-console /app/bin/paladin-console

USER ${APP_UID}:${APP_GID}

EXPOSE ${APP_PORT}

# For `docker run` and compose; Kubernetes ignores it and uses the chart's
# probes. The binary asks itself: distroless has no shell or wget. The liveness
# route touches no backend, so a backend outage does not mark the console
# unhealthy.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD ["/app/bin/paladin-console", "probe"]

ENTRYPOINT ["/app/bin/paladin-console"]
