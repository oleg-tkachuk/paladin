# ADR-0049: The console ships as a static export behind a small Go server

- **Status:** Accepted 2026-11-13.
- **Related:** [ADR-0048](0048-the-console-holds-no-token.md) (what left the
  console's server), [ADR-0009](0009-oauth-authorization-server.md) (the
  consent form the console relays).

## Context

After ADR-0048 the console's Next.js server holds no token and calls no plane
on the browser's behalf. Every page is a client component; the server renders
a shell the browser then fills through the session role. What the Node
process still does is small:

- `src/proxy.ts`: sets the CSP, which names the deployment's storage origins
  and so is known only where the image runs; redirects a request without the
  session cookie to `/login?next=…` (a UX gate, not the boundary); rejects a
  cross-origin state-changing call to `/api/`.
- `next.config.ts` `headers()`: HSTS, `X-Frame-Options`,
  `X-Content-Type-Options`, `Referrer-Policy`, `X-DNS-Prefetch-Control`,
  `Permissions-Policy`.
- `GET /api/banner`: the operator's banner, from `PALADIN_BANNER_*`.
- `GET /api/health/live`: the kubelet probe.
- `POST /oauth/authorize`: relays the consent form to the IAM plane.
- Three pages that only redirect from old paths (`/ops`, a backend's
  `buckets`, a tenant bucket's `[[...tab]]`).

For that, every console pod runs Node with the standalone server and its
`node_modules`: a larger image, a slower start, and a runtime whose
dependencies are patched on Next's schedule rather than ours.

A spike built the console with `output: "export"`. It builds; the CSP needs
no nonce (`script-src 'self' 'unsafe-inline'`), so it is unaffected. Dynamic
segments are exported once, under a placeholder `_`, through
`generateStaticParams`. With `output: "export"` set, `next dev` answers 404
for any other value of the segment; set only for the production build, dev
renders every value. On a deep link to
`/tenants/abc-123/billing` served from `tenants/_/billing.html`,
`usePathname()` returns the real path but `useParams()` returns
`{ id: "_" }`: the params are baked into the page at build time.

## Decision

1. **The console's production build uses `output: "export"`.** No Node
   process runs in the console's image. Each dynamic segment exports one page
   under the placeholder `_`. `next dev` runs without it, so development
   serves every path as today.
2. **Route params are read from the path, not from `useParams`.** One hook,
   `useRouteParams`, matches `usePathname()` against the route's pattern and
   returns them decoded; the pages and layouts that read `useParams` move to
   it. A test keeps `useParams` out of the source and checks that every
   pattern a call site spells is a directory under `src/app`.
3. **A Go server in the console's image serves the export**, embedded with
   `go:embed`. The export's own tree is its route table: a request path is
   resolved segment by segment, a literal entry first and `_` otherwise, and a
   path no route matches gets the export's 404 page. The client router's RSC
   requests carry the `RSC` header and ask for the page's path plus `.txt`;
   the header, not the suffix, tells them from a page whose id ends in `.txt`.
   It takes over everything the Node process does today:
   - the security headers and the CSP, with the storage origins from
     `PALADIN_STORAGE_ORIGINS` and the consent page's `form-action`;
   - the cookie gate to `/login?next=…`, with the same public prefixes;
   - `GET /api/banner`, `GET /api/health/live`;
   - the `POST /oauth/authorize` relay to `PALADIN_IAM_URL`, returning the
     AS's answer unfollowed, as today;
   - the three legacy redirects, which leave `src/app`.

   The `Origin` check does not move: it guarded state-changing calls to the
   console's `/api/` routes, and none is left. The server takes GET and HEAD
   everywhere, POST only on `/oauth/authorize` (which the Node server did not
   check either), and answers 405 to anything else.
4. **The server is its own Go module, `frontend/server`.** It imports nothing
   from `backend/`, so the console's image never links the backend's
   dependencies; it uses the standard library and zap. A dependency test pins
   its import set. It logs start, stop, every request (DEBUG; WARN for a
   refused method, ERROR for a failure) and each relayed consent post, with
   the backend's field names.
5. **Names both sides must spell alike live in one file.**
   `server/contract/contract.json` holds the session cookie's name, the paths
   the server answers, the public prefixes and the placeholder. The Go
   server embeds it; the pages import it as JSON. A backend test checks the
   session role's cookie name against it, and a server test checks the chart
   sets every variable the server reads.
6. **The chart's contract stays**: port, probe path, environment variable
   names and the backend CA mount. `NODE_EXTRA_CA_CERTS` gives way to
   `PALADIN_BACKEND_CA_FILE`, which the chart sets from the same mount.

## Options considered

- **Keep the standalone Node server.** No work, and Node stays in every pod
  for a proxy, two static routes and a relay.
- **Move these routes into the Go session role** and serve the export from a
  plain file server. One Go server fewer, but the session role would then own
  UI concerns (CSP, banner, page routing), and the console could not be
  deployed or rolled back apart from it.
- **A static export behind a generic web server** (nginx, Caddy). The
  storage origins in the CSP, the cookie gate and the consent relay become
  web-server configuration with no tests; the route table would be repeated
  in it.

## Consequences

- The console's image is a Go binary with the export inside it; the
  `node_modules` of the standalone server leave the runtime.
- A page cannot read data on the server any more. None does today; a future
  page that wants to must fetch from the browser.
- `next dev` keeps working for development, `useRouteParams` reading the
  real path there as in the export. Without the Go server in front, dev has
  no cookie gate, CSP or banner route; the e2e stack runs the built image, so
  those are exercised there.
- A new dynamic route needs its placeholder `generateStaticParams`; the build
  fails without it, which is the reminder.
- Measured before this decision, first-load JS per page is 419–468 KiB gzip
  after the move to `zod/mini`; this decision changes the server, not that
  number.
