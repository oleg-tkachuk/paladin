# syntax=docker/dockerfile:1
#
# PALADIN UI container image — three stages:
#
#   1. deps    — installs npm dependencies from a clean lockfile on
#                BUILDPLATFORM. Cached when package*.json don't change.
#   2. builder — runs `next build` against the standalone output target
#                so the runner image carries only the trimmed server.
#   3. runner  — node:alpine with the standalone bundle, dropping perms
#                to a non-root nextjs user.
#
# The Helm chart mounts the runtime config at /app/configs via
# ConfigMap, never baked into the image. Build metadata (APP_VERSION /
# GIT_COMMIT_HASH / BUILD_TIME) is threaded through as build-args from
# Taskfile's metadata:write step.

ARG NODE_VERSION=25
ARG NPM_VERSION=11.13.0

# -----------------------------------------------------------------------------
# Stage 1 — deps. Pinned to BUILDPLATFORM so a multi-arch run doesn't
# re-fetch npm trees per arch (the resulting node_modules are pure JS).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS deps
ARG NPM_VERSION

# libc6-compat keeps prebuilt addons (sharp, swc) loadable on Alpine's musl.
RUN apk add --no-cache libc6-compat
WORKDIR /app

RUN npm install -g npm@${NPM_VERSION}

# Copy lockfiles first so this layer caches across source-only changes.
COPY package.json package-lock.json ./
RUN npm ci

# -----------------------------------------------------------------------------
# Stage 2 — builder. Runs `next build` with build-time metadata baked in
# via NEXT_PUBLIC_-prefixed env vars (Next inlines them into the bundle).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS builder
ARG NPM_VERSION

WORKDIR /app
RUN npm install -g npm@${NPM_VERSION}
COPY --from=deps /app/node_modules ./node_modules
COPY . .

# Build-time metadata. `undefined` defaults keep the image buildable from
# `docker build .` without Taskfile threading the values through.
ARG APP_VERSION=undefined
ARG GIT_COMMIT_HASH=undefined
ARG BUILD_TIME=undefined

ENV NEXT_TELEMETRY_DISABLED=1 \
    APP_VERSION=${APP_VERSION} \
    GIT_COMMIT_HASH=${GIT_COMMIT_HASH} \
    BUILD_TIME=${BUILD_TIME}

RUN npm run build

# -----------------------------------------------------------------------------
# Stage 3 — runner. Distroless-style minimal layout: only what the
# standalone bundle needs, dropped to UID 1001 for non-root execution.
# -----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine AS runner
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
