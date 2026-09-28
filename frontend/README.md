# Paladin console

Next.js admin console for the Paladin, plus the BFF that
sits between the browser and the control plane's Connect-RPC services.

The browser never talks to a plane directly. Requests go to the BFF at
`/api/paladin`, which holds the upstream URLs, attaches credentials, and
forwards over Connect. That is why the plane addresses are server-side
configuration and not `NEXT_PUBLIC_*` values.

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

## Authentication in development

The console sends whatever bearer sits in `localStorage.paladin_token`, which you
set through its own `/config` page. There is no browser-side OIDC flow and no
`disableAuth` switch in the code — the dev config's `oidc` block described a
design that never landed and has been removed.

`configs/config.yaml`'s `auth.devToken` has one reader left,
`scripts/dev-bootstrap.sh`, which greps it for a JWT to seed a local stack with.
**It ships empty on purpose** — this repository is public, so a committed token
is a token everyone holds.

Mint one for your stack and paste it into the console's `/config` page:

```bash
task backend:auth:mint-token
```

Roles in a token must use the dotted form (`platform.admin`). The
hyphenated spelling parses fine and then fails every Cedar policy check,
which looks exactly like a permissions bug.

## Layout

```
frontend/
├── src/
│   ├── app/              routes — one directory per console surface
│   │   ├── api/          the BFF: /api/paladin RPC proxy, auth, health
│   │   ├── tenants/      tenants and everything scoped under one
│   │   ├── buckets/      buckets, objects, object keys, trash, upload
│   │   ├── policies/     Cedar policy editing and simulation
│   │   ├── mcp/          MCP upstream inspection
│   │   ├── audit/  billing/  stats/  ops/  health/
│   │   └── storage-backends/  users/  oauth/  login/  config/
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
pnpm run generate        # reads ../backend/proto, then prettier
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
