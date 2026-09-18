# syntax=docker/dockerfile:1
#
# Paladin UI container image — three stages:
#
#   1. deps    — installs pnpm dependencies from a clean lockfile on
#                BUILDPLATFORM. Cached when package.json / lockfile
#                don't change.
#   2. builder — runs `next build` against the standalone output target
#                so the runner image carries only the trimmed server.
#   3. runtime — node:alpine with the standalone bundle, dropping perms
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
ARG NODE_VERSION=26
# renovate: datasource=npm depName=pnpm
ARG PNPM_VERSION=12.4.2

# -----------------------------------------------------------------------------
# Stage 1 — deps. Pinned to BUILDPLATFORM so a multi-arch run doesn't
# re-fetch the pnpm content-addressable store per arch (the resulting
# node_modules are pure JS).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine@sha256:aadf416b2cdce311a8811ba3f0608a61b77dbf997500e2eafe781b51f6a0b019 AS deps
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
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine@sha256:aadf416b2cdce311a8811ba3f0608a61b77dbf997500e2eafe781b51f6a0b019 AS builder
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
# Stage 3 — runtime. Minimal layout: only what the standalone bundle
# needs, dropped to UID 1001 for non-root execution.
# -----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine@sha256:aadf416b2cdce311a8811ba3f0608a61b77dbf997500e2eafe781b51f6a0b019 AS runtime

# ARGs do not cross stage boundaries — the builder's copies above are out of
# scope here and have to be redeclared. Same block as backend/deploy/Dockerfile.
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

# Which commit is this image? Until now the answer was "nobody can tell": the
# console image carried the metadata as ENV inside the build stage and no OCI
# labels at all, so `docker image inspect` reported no revision. That is the
# missing half of every stale-image incident in this repository — a Playwright
# run reported against a console nobody could attribute. `task verify-e2e`
# prints these before it starts.
LABEL org.opencontainers.image.title="paladin-console"       org.opencontainers.image.description="Paladin — admin console"       org.opencontainers.image.version="${APP_VERSION}"       org.opencontainers.image.revision="${GIT_COMMIT_HASH}"       org.opencontainers.image.created="${BUILD_TIME}"       org.opencontainers.image.source="https://github.com/oleg-tkachuk/paladin"

# The base image lags alpine's own repository on security patches: node:26-alpine
# currently ships libcrypto3/libssl3 3.5.7-r0 while alpine 3.24 has published
# 3.5.8-r0 for CVE-2026-14456 (openssl DoS, HIGH). Bumping the digest above does
# not help — the newest node:26-alpine still carries 3.5.7-r0, checked before
# writing this — so the patch has to come from apk.
#
# Two named packages, not a blanket `apk upgrade`: the digest pin above exists
# for reproducibility, and this narrows what varies between builds to the thing
# a scanner is failing on. Drop it once the base image catches up.
RUN apk upgrade --no-cache libcrypto3 libssl3

WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

# Dedicated non-root user. UID/GID 1001 matches the Helm chart's
# `runAsUser` / `runAsGroup` so bind-mounted volumes share owner with
# the in-cluster pod.
#
# --ingroup nodejs, so nextjs is actually a member of it. Without it busybox
# gives the user a private `nextjs` group — verified on the shipped image:
# `id nextjs` reported gid=65533(nogroup) and `nodejs` had no members at all.
# Every `--chown=nextjs:nodejs` below was then granting to a group the user was
# not in, and the files stayed reachable only through their owner bits. It
# happened to work; it was not what any of these lines say.
#
# In-cluster it worked for a second accidental reason: the chart sets
# runAsGroup: 1001, which supplies at run time the membership the image failed
# to build in. An image that only behaves under one chart's securityContext is
# a dependency nobody wrote down.
#
# Also drop the global npm/npx CLI the node base image bundles: the Next.js
# standalone runtime runs `node server.js` and never invokes it, but npm ships
# with its own transitive deps (brace-expansion/tar/undici) that Trivy flags as
# HIGH/CRITICAL. Removing it keeps those CVEs out of the shipped image without
# affecting the runtime (the build stages keep their own npm).
RUN addgroup --system --gid 1001 nodejs && \
    adduser  --system --uid 1001 --ingroup nodejs nextjs && \
    rm -rf /usr/local/lib/node_modules/npm /usr/local/bin/npm /usr/local/bin/npx

COPY --from=builder /app/public ./public

# Prerender cache — created up-front with correct ownership so the runtime
# needs no write permission on /app itself. `.next/cache` explicitly, not just
# `.next`: Next creates it on the first ISR write, which succeeds only because
# the parent happens to be owned by the app user. Making it here means the
# runtime never depends on that.
RUN mkdir -p .next/cache && chown -R nextjs:nodejs .next

# Standalone output (https://nextjs.org/docs/advanced-features/output-file-tracing)
# trims the runtime to only the modules `next build` actually traced.
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs:nodejs

EXPOSE 3000

# server.js is emitted by `next build` when output: "standalone" is set
# in next.config.ts. It's the entrypoint for the trimmed bundle.
CMD ["node", "server.js"]
