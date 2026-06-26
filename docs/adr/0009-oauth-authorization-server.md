# ADR-0009: OAuth 2.1 Authorization Server (IAM-as-AS)

- **Status:** Proposed — implementation in progress. Foundation (storage,
  PKCE, config) landed 2026-06-27; endpoints + consent UI follow. Supersedes
  the AS half of the BACKLOG "OAuth … for MCP clients" entry; builds on the
  Resource-Server half in [ADR-0008](0008-mcp-oauth-resource-server.md).
- **Context:** ADR-0008 made the MCP server a spec-compliant OAuth Resource
  Server — it advertises where to authenticate and validates bearers — but
  nothing *mints* those bearers via a browser auth-code flow. Standard MCP
  clients (Claude Desktop / Cursor) open a browser at the AS's `/authorize`,
  consent, and exchange a code for a token. We choose **PALADIN IAM as the
  Authorization Server** (not a federated IdP) so the agentic plane has a
  self-contained auth story; the federated-OIDC option (Phase 5b.1) stays
  open and is cheaper later because the discovery + endpoint shapes match.

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
  against PALADIN, with the bearer the ADR-0008 RS already validates.
- Audience binding is explicit: the access token's `aud` is the resource the
  client requested, so it works on exactly the plane(s) the MCP profile
  targets — same single-audience reality as today, now spec-driven.
- The server-rendered login/consent is intentionally minimal (no i18n). A
  polished Next.js consent page (`frontend/src/app/oauth/consent`) is a
  follow-up; the backend contract it posts to is stable.
- Federated OIDC (Phase 5b.1) remains a clean future swap: the RS half is
  identical, and the AS endpoints map onto an external IdP's equivalents.
- **Deferred within this ADR:** the polished frontend consent page, refresh
  token family/repudiation analytics, and per-client scope-grant memory
  (skip re-consent). Tracked in BACKLOG.
