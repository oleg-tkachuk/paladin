# Paladin console

Next.js admin console for Paladin. It renders the UI; the session is held by
the backend's session role, `paladin serve session` (ADR-0048).

The browser never talks to a plane directly and holds no token. Its Connect
calls go to `/session/rpc/<package.Service>/<Method>` on the console's own
origin (`src/lib/connect/transport.ts`), where the ingress — or Caddy in
compose — routes `/session/` to the session role. The role forwards each call
to the plane its proto package names, with an access token it keeps.

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

`pnpm dev` alone renders the pages but cannot sign in: `/session/` has to be
served on the same origin. Front it as `backend/deploy/front/Caddyfile` does,
with `/session/*` to a session role and the rest to the dev server. The
server's one upstream is `PALADIN_IAM_URL`, for the OAuth consent relay; it
falls back to an in-cluster service name.

## Authentication

Sign in at `/login` with a Paladin user — on a fresh stack, `admin` and the
bootstrap password. The session role keeps the refresh token in the httpOnly
`__Host-paladin_session` cookie and rotates it once it is `session.rotate_after`
(5 minutes) old; the browser never sees a token. `AuthContext` signs in, out
and between tenants through `src/lib/session/client.ts`, and learns who is
signed in from `WhoAmI`.

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
│   │   ├── api/          the console's own routes: banner, health/live
│   │   ├── tenants/      tenants and everything scoped under one
│   │   ├── buckets/  collections/  upload/  trash/
│   │   ├── policies/     Cedar policy editing and simulation
│   │   ├── mcp/          MCP upstream inspection
│   │   ├── audit/  billing/  stats/  ops/  operations/  health/
│   │   ├── oauth/        consent page; authorize relays the form to iam
│   │   └── storage-backends/  users/  applications/  profile/  login/  config/
│   ├── components/       shared UI (shadcn/ui + Tailwind)
│   ├── context/          React providers: auth, scope, shell, stats, actions,
│   │                     breadcrumb names, refresh
│   ├── hooks/            data hooks; error contract in hooks/README.md
│   ├── constants/        API contract constants, role names
│   ├── gen/              GENERATED Connect-ES stubs — do not hand-edit
│   ├── lib/              client helpers, formatting; connect/ (the transport
│   │                     to the session role), session/ (its endpoints)
│   ├── proxy.ts          CSP, /login without a session cookie, Origin check
│   │                     on unsafe /api calls
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

`pnpm run test:e2e` brings its own stack up — Postgres, SeaweedFS and the
Paladin planes — and leaves it running for the next run;
`pnpm run test:e2e:stack:down` removes it. It needs nothing but Docker and the
two locally built images; see [`tests/e2e/README.md`](tests/e2e/README.md).
