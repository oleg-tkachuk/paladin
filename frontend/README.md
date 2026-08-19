# PALADIN console

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
task e2e-up
```

Against a backend you are already running, with hot reload:

```bash
cd frontend
pnpm install
pnpm dev            # http://localhost:3000
```

`configs/config.yaml` points the BFF at its upstreams. The defaults are
in-cluster service DNS, so for a locally running backend you will want to
override `runtimeConfig.public.objectControlPlane.upstreamUrl` and the
`admin` / `iam` URLs.

## Authentication in development

`oidc.disableAuth: true` in the dev config turns off browser-side auth,
and the BFF falls back to `runtimeConfig.public.auth.devToken` when
`localStorage.paladin_token` is empty. **`devToken` ships empty on purpose** —
it is served to the browser, and this repository is public.

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
│   └── config.ts         loads configs/config.yaml, validated with zod
├── configs/config.yaml   runtime config, read server-side at boot
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
MinIO and the PALADIN planes. It needs nothing but Docker and the two locally
built images; see [`tests/e2e/README.md`](tests/e2e/README.md).
