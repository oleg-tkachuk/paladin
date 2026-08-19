# ADR-0009: OAuth 2.1 Authorization Server (IAM-as-AS)

- **Status:** Accepted — fully implemented 2026-06-27 (storage, PKCE,
  endpoints, consent UI, Cedar gating, rate-limit/CORS, refresh
  reuse-detection with per-family precision, trusted-client skip-consent, and
  an end-to-end HTTP integration test). Supersedes the AS half of the BACKLOG
  "OAuth … for MCP clients" entry; builds on the Resource-Server half in
  [ADR-0008](0008-mcp-oauth-resource-server.md).
- **Context:** ADR-0008 made the MCP server a spec-compliant OAuth Resource
  Server — it advertises where to authenticate and validates bearers — but
  nothing *mints* those bearers via a browser auth-code flow. Standard MCP
  clients (Claude Desktop / Cursor) open a browser at the AS's `/authorize`,
  consent, and exchange a code for a token. We choose **Paladin IAM as the
  Authorization Server** (not a federated IdP) so the agentic plane has a
  self-contained auth story; the federated-OIDC option (Phase 5b.1) is **not
  on the roadmap** (Paladin is engineer-operated and owns its IAM — withdrawn
  2026-06-30, see [ADR-0006](0006-deferred-roadmap.md)) but stays a cheap
  future swap because the discovery + endpoint shapes match.

  Two facts from the codebase shape the design:
  - **Single-audience tokens.** A JWT carries one `aud`; the three planes
    (`paladin-data` / `paladin-admin` / `paladin-iam`) each enforce it. The bridge
    forwards one bearer, so an OAuth access token must be minted for a
    specific audience (resource→audience binding, RFC 8707).
  - **No browser session.** IAM authenticates by bearer-in-header; there is
    no cookie session. So the AS must render its **own** login + consent
    server-side for the browser leg — it can't lean on an existing session.

## Decision

Implement a minimal, spec-correct OAuth 2.1 AS in the IAM plane, mounted as
raw HTTP (RFC 6749 is form-encoded, not Connect), gated by `auth.oauth.enabled`.

- **`GET/POST /oauth/authorize`** — the browser leg. Server-rendered login
  (reuses the existing user store + `auth.CheckPassword`) then a consent
  screen listing the client + requested scopes. On approve, issues a
  single-use authorization `code` bound to a PKCE `code_challenge` (S256
  mandatory) and redirects to the registered `redirect_uri`.
- **`POST /oauth/token`** — exchanges `code + code_verifier` for an access
  token (minted via the existing `issuer.Issuer` with `aud` = the requested
  resource's audience) plus a rotating refresh token (stored in
  `refresh_tokens`, reusing the login rotation path). Also serves
  `grant_type=refresh_token`. PKCE verified; codes are single-use.
- **`POST /oauth/register`** (RFC 7591) — dynamic client registration,
  flag-gated (`auth.oauth.dynamic_registration`); first-party clients
  (`claude-desktop`, `cursor`) may be pre-seeded in config. Confidential
  clients get a `client_secret` hashed with the existing bcrypt helper.

**Storage** (migration 043, pgx-backed store):
- `oauth_clients` — id, redirect_uris[], allowed_scopes[], allowed_audiences[],
  secret_hash (NULL = public), is_public, created_at.
- `oauth_authorization_codes` — code_hash (PK), client_id, user_id, tenant_id,
  redirect_uri, code_challenge, scopes[], audience, expires_at (≤60s),
  consumed_at (single-use guard).
- Refresh tokens reuse the existing `refresh_tokens` table + rotation.

**Security invariants:** codes single-use (atomic `consumed_at`), refresh
rotated every grant (RFC 6749 §6), PKCE S256 required for public clients,
`client_secret` bcrypt-hashed, `/oauth/token` rate-limited per client via the
existing api-token limiter, redirect_uri exact-match against the registration.

**Cedar gating:** an `AuthorizeOAuth` action lets admins control which roles
may grant which scopes to which clients.

## Consequences

- Standard MCP clients get an end-to-end browser auth-code + PKCE flow
  against Paladin, with the bearer the ADR-0008 RS already validates.
- Audience binding is explicit: the access token's `aud` is the resource the
  client requested, so it works on exactly the plane(s) the MCP profile
  targets — same single-audience reality as today, now spec-driven.
- The server-rendered login/consent is intentionally minimal (no i18n). A
  polished Next.js consent page (`frontend/src/app/oauth/consent`) is a
  follow-up; the backend contract it posts to is stable.
- Federated OIDC (Phase 5b.1) is not on the roadmap (withdrawn 2026-06-30,
  [ADR-0006](0006-deferred-roadmap.md)) but remains a clean future swap if a
  customer ever mandates SSO: the RS half is identical, and the AS endpoints
  map onto an external IdP's equivalents.
- **Authz, as shipped:** consent is gated on (1) resource-owner
  authentication, (2) the requested scopes being a subset of the client's
  registered grant, and (3) a Cedar `AuthorizeOAuth` check. The built-in
  policy *permits* `AuthorizeOAuth` for any authenticated principal (standard
  OAuth self-consent), so the flow works out of the box; a tenant policy can
  `forbid` it for specific principals/clients/scopes (first-forbid wins) via
  `context.oauth_client_id` / `context.oauth_scopes`. This default-permit +
  forbiddable shape sidesteps Cedar's default-deny without needing per-tenant
  permit templates. The engine is injected optionally (`WithAuthorizer`); a
  nil authorizer falls back to (1)+(2) so the AS still runs standalone.
- **Consent UI:** shipped both ways. The built-in server-rendered form works
  standalone; when `auth.oauth.consent_url` is set, `/authorize` redirects to
  the polished Next.js page (`frontend/src/app/oauth/consent`) which POSTs the
  credentials + decision back. The form is a native cross-origin POST (not
  fetch) so the browser follows the backend's 302 to the client redirect_uri
  — including custom schemes like `claude-desktop://`.
- **Hardening, as shipped:** `/oauth/token` is rate-limited per `client_id`
  (in-memory token bucket, `token_rate_limit_per_minute`, default 60/min →
  429 + Retry-After) and supports CORS for browser public clients
  (`token_endpoint_allowed_origins`; OPTIONS preflight + ACAO). The full
  authorize→token→refresh chain has an in-process integration test.
- **Refresh-token reuse-detection, as shipped:** refresh tokens carry a
  `family_id` (migration 044) — a login starts a family, each rotation
  inherits it. Replaying a rotated (revoked) token at either refresh path
  (OAuth `/oauth/token` or Connect `AuthService.RefreshToken`/
  `ExchangeAudience`) revokes only that **family** (`RevokeFamilyOf`, RFC
  6819 — the compromised chain, not the user's other sessions), logs a
  warning, and writes an audit row (`iam.RefreshTokenReuseDetected`,
  is_error) that surfaces — highlighted — in the admin audit console.
- **Skip-consent (trusted clients):** seed clients may set `skip_consent` —
  the operator pre-authorizes a first-party app (claude-desktop/cursor), so
  `/authorize` renders a plain login with no per-user consent screen. Never
  available to dynamically-registered clients. This is the
  sessionless-appropriate form of "skip re-consent": Paladin has no browser
  session, so per-user remembered consent can't skip the login step anyway —
  the operator grants consent once via config instead. Per-user remembered
  consent is revisitable if/when a browser session lands (ADR-0006 Phase
  5b.1 / OIDC).
- **End-to-end test:** `internal/integration/oauth_e2e_test.go`
  (`-tags=integration`, testcontainers) drives authorize → token → refresh +
  single-use replay over real HTTP against real Postgres. This supersedes the
  live-stack hurl variant, which needs a full compose stack + a password user
  the e2e harness doesn't provision.
