# syntax=docker/dockerfile:1
#
# PALADIN UI container image — three stages:
#
#   1. deps    — installs pnpm dependencies from a clean lockfile on
#                BUILDPLATFORM. Cached when package.json / lockfile
#                don't change.
#   2. builder — runs `next build` against the standalone output target
#                so the runner image carries only the trimmed server.
#   3. runner  — node:alpine with the standalone bundle, dropping perms
#                to a non-root nextjs user.
#
# The Helm chart mounts the runtime config at /app/configs via
# ConfigMap, never baked into the image. Build metadata (APP_VERSION /
# GIT_COMMIT_HASH / BUILD_TIME) is threaded through as build-args from
# Taskfile's metadata:write step.
#
# Package manager: pnpm. Version pinned to the `packageManager` field
# in package.json (Corepack reads it). Mirrors the local-dev path
# (see specs/001-frontend-playwright-e2e research.md R-008).

# NODE_VERSION is the human-readable tag; the @sha256 digest on each FROM is
# the authoritative pin (a tag is mutable, a digest is not). Renovate keeps
# both in lockstep — see renovate.json. Bump the tag and digest together.
ARG NODE_VERSION=24
ARG PNPM_VERSION=11.3.0

# -----------------------------------------------------------------------------
# Stage 1 — deps. Pinned to BUILDPLATFORM so a multi-arch run doesn't
# re-fetch the pnpm content-addressable store per arch (the resulting
# node_modules are pure JS).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine@sha256:156b55f92e98ccd5ef49578a8cea0df4679826564bad1c9d4ef04462b9f0ded6 AS deps
ARG PNPM_VERSION

# libc6-compat keeps prebuilt addons (sharp, swc) loadable on Alpine's
# musl. `node:${VERSION}-alpine` ships npm but NOT corepack since
# upstream removed it from official images — install pnpm directly to
# match the local-dev pin in package.json's `packageManager` field.
RUN apk add --no-cache libc6-compat && \
    npm install -g pnpm@${PNPM_VERSION}
WORKDIR /app

# Copy lockfile + manifest first so this layer caches across source-
# only changes. pnpm-workspace.yaml holds the `allowBuilds` security
# gate (see frontend/pnpm-workspace.yaml).
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./

# `--frozen-lockfile` is the CI-safe install: fails fast if
# pnpm-lock.yaml is out of date instead of silently re-resolving.
RUN pnpm install --frozen-lockfile

# -----------------------------------------------------------------------------
# Stage 2 — builder. Runs `next build` with build-time metadata baked in
# via NEXT_PUBLIC_-prefixed env vars (Next inlines them into the bundle).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine@sha256:156b55f92e98ccd5ef49578a8cea0df4679826564bad1c9d4ef04462b9f0ded6 AS builder
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
# Stage 3 — runner. Distroless-style minimal layout: only what the
# standalone bundle needs, dropped to UID 1001 for non-root execution.
# -----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine@sha256:156b55f92e98ccd5ef49578a8cea0df4679826564bad1c9d4ef04462b9f0ded6 AS runner
WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

# Dedicated non-root user. UID/GID 1001 matches the Helm chart's
# `runAsUser` / `runAsGroup` so bind-mounted volumes share owner with
# the in-cluster pod.
RUN addgroup --system --gid 1001 nodejs && \
    adduser  --system --uid 1001 nextjs

COPY --from=builder /app/public ./public

# Prerender cache directory — created up-front with correct ownership so
# the runtime doesn't need write perms on /app itself.
RUN mkdir .next && chown nextjs:nodejs .next

# Standalone output (https://nextjs.org/docs/advanced-features/output-file-tracing)
# trims the runtime to only the modules `next build` actually traced.
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs

EXPOSE 3000

# server.js is emitted by `next build` when output: "standalone" is set
# in next.config.ts. It's the entrypoint for the trimmed bundle.
CMD ["node", "server.js"]
