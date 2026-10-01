# Paladin console

Next.js admin console for Paladin, plus the BFF that sits between the browser
and the control plane's Connect-RPC services.

The browser never talks to a plane directly. Requests go to the BFF at
`/api/rpc/{data,iam,admin}`, which holds the upstream URLs and forwards over
Connect. That is why the plane addresses are server-side configuration and not
`NEXT_PUBLIC_*` values.

## Running it

Against a full local stack (backend planes, Postgres, storage, and this
console, all in compose) — from the repository root:

```bash
task stack:up
```

Against a backend you are already running, with hot reload:

```bash
cd frontend
pnpm install
pnpm dev            # http://localhost:3000
```

The BFF reaches its upstreams through `PALADIN_DATA_URL`, `PALADIN_IAM_URL` and
`PALADIN_ADMIN_URL`; each falls back to an in-cluster service name, so point them
at a locally running backend by setting them in your shell. `configs/config.yaml`
used to carry those URLs and no longer does — nothing read them from there.

## Authentication

Sign in at `/login` with a Paladin user — on a fresh stack, `admin` and the
bootstrap password. The BFF keeps the refresh token in an httpOnly cookie and
hands the browser short-lived access tokens, one per plane, held in memory
(`src/lib/auth/tokenStore.ts`); `/api/auth/me` rotates the refresh token once
it is five minutes old (`src/lib/auth/rotation.ts`).

`configs/config.yaml`'s `auth.devToken` is read only by
`scripts/dev-bootstrap.sh`, which uses it to seed a local stack. It ships
empty: this repository is public. To fill it for your stack:

```bash
task backend:auth:mint-token   # from the repository root
```

Roles in a token use the dotted form (`platform.admin`). The hyphenated
spelling parses and then fails every Cedar policy check.

## Layout

```
frontend/
├── src/
│   ├── app/              routes — one directory per console surface
│   │   ├── api/          the BFF: /api/rpc proxy, auth, health, audit stream
│   │   ├── tenants/      tenants and everything scoped under one
│   │   ├── buckets/  collections/  upload/  trash/
│   │   ├── policies/     Cedar policy editing and simulation
│   │   ├── mcp/          MCP upstream inspection
│   │   ├── audit/  billing/  stats/  ops/  health/
│   │   └── storage-backends/  users/  profile/  oauth/  login/  config/
│   ├── components/       shared UI (shadcn/ui + Tailwind)
│   ├── gen/              GENERATED Connect-ES stubs — do not hand-edit
│   ├── lib/              client helpers, auth plumbing, formatting
│   └── config.ts         loads configs/config.yaml (uiMetadata only), via zod
├── configs/config.yaml   dev-bootstrap's JWT; the app reads only uiMetadata
├── tests/e2e/            Playwright suite + its compose stack
└── deploy/               Dockerfile and Helm chart
```

## Generated code

`src/gen/` is regenerated from the backend's protobuf definitions:

```bash
pnpm run generate        # reads ../proto, then prettier
```

A proto change is a two-sided change. That is the main reason both halves
live in one repository.

## Checks

```bash
pnpm test                # vitest, unit
pnpm run test:e2e        # Playwright against the compose stack (needs Docker)
pnpm lint
npx tsc --noEmit
```

`pnpm run test:e2e` brings its own stack up and tears it down — Postgres,
SeaweedFS and the Paladin planes. It needs nothing but Docker and the two locally
built images; see [`tests/e2e/README.md`](tests/e2e/README.md).
