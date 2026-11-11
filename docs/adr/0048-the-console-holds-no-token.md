# ADR-0048: The console renders the UI, and a Go session edge holds the session

- **Status:** Proposed 2026-11-11.
- **Related:** [ADR-0047](0047-sessions-signed-by-iam-alone.md) (the session
  keys this builds on), [ADR-0033](0033-console-separates-platform-from-tenant-workspace.md).

## Context

The console's Next.js server is more than a renderer. It is a token
handler:

- It holds the refresh token in its cookie (`paladin_rt_iam`), decides when
  to rotate it (`lib/auth/rotation.ts`, by the token's `iat`, unverified),
  and mints per-plane access tokens through `ExchangeAudience`, choosing the
  audience itself for login, `/me`, memberships, tenant switching, logout
  and the audit stream.
- It hands those access tokens to the browser, which keeps them in memory,
  shares them across tabs and sets `Authorization` itself.
- Its RPC bridge (`app/api/rpc`) is a Connect server that registers all 27
  services by hand and re-encodes every call, and it decodes each token's
  `aud`, unverified, to gate the plane.
- It runs the CSRF check, and two aggregators: `/api/shell` (four RPCs, two
  tokens) and `/api/health/all`, which fetches six roles' health with the
  shared health-snapshot token — the one secret the Next.js server holds.

So session logic lives in two languages, an access token is readable by any
script that runs in the page, a new RPC is unreachable from the console
until the bridge learns it, and a UI process holds a backend secret.

## Decision

1. **The Next.js server renders the UI and serves its assets, nothing
   else.** It holds no token, no cookie of the session, and no backend
   secret, and decodes no JWT.
2. **A Go session edge, `serve console-edge`, terminates the console's
   session.** It sets and reads the session cookie (`__Host-`, httpOnly,
   Secure, SameSite=Strict), holds the refresh token behind it, rotates it,
   exchanges it through IAM for the access token each plane needs, keeps
   those in memory until they expire, and attaches them to the calls it
   forwards. It holds no signing key: it is a client of IAM, verified by
   the planes like any other. It is not the IAM role, so console traffic —
   uploads included — never runs where the private key is.
3. **The edge forwards Connect calls as bytes.** It routes by service name
   to the plane that serves it and proxies the request unchanged, so a new
   RPC needs no change to the edge or the console.
4. **The browser holds no token.** It calls the edge with the cookie; the
   edge checks the `Origin` for every state-changing call, the CSRF check
   the Next.js proxy runs today.
5. **Aggregation moves to the backend or the browser.** Platform health
   becomes an admin-plane RPC that fans out to the roles, so the health
   token leaves the console; the shell's four calls are made by the browser
   through the edge.
6. **The audit stream is proxied by the edge** like any other call.

## Consequences

- An XSS in the console no longer reads an access token; it can still act
  through the cookie while the page is open, which `Origin` checks and the
  CSP bound.
- Session handling has one implementation, in the language of the IAM it
  calls, with the backend's logging and tests.
- One more role to deploy. The ingress routes the console's API paths to
  the edge and everything else to Next.js, on one origin.
- The console's auth routes, `tokenStore`, `jwtAudience.ts`, `rotation.ts`
  and the RPC bridge are deleted, not ported.
