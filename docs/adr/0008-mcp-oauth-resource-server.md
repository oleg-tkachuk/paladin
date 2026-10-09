# ADR-0008: MCP server as an OAuth 2.1 Resource Server

- **Status:** Accepted (RS phase implemented 2026-06-27). The AS phase it
  deferred is superseded by [ADR-0009](0009-oauth-authorization-server.md),
  implemented 2026-06-27.
- **Context:** The streamable-HTTP MCP server authenticated requests with a
  non-standard `X-Paladin-Token` header — a bearer the operator pasted into the
  agent host's config. Standard MCP clients (desktop agents, IDE plugins)
  implement the MCP Authorization spec (OAuth 2.1): they send
  `Authorization: Bearer`, and on a 401 they expect a `WWW-Authenticate`
  challenge that points at OAuth metadata so they can *discover* where to
  authenticate and run an auth-code + PKCE flow. Against the old server they
  could not connect — a missing/absent token got a flat `400` with no
  discovery hint.

  Building Paladin into a full OAuth Authorization Server (browser consent,
  `/authorize` + `/token`, dynamic client registration, PKCE, code storage)
  is a large, multi-part effort with a frontend consent screen. The
  Resource-Server half — discovery metadata + a spec-compliant challenge +
  edge token validation — is small, self-contained, and unblocks standard
  clients against *any* Authorization Server (Paladin IAM later, or a federated
  IdP now). So we split the work and ship the RS half first.

## Decision

Make the MCP server a spec-compliant OAuth 2.1 **Resource Server**, gated by
`cfg.MCP.OAuth.Enabled` (off by default; the legacy `X-Paladin-Token` path is
unchanged when off):

- **Discovery.** Serve `/.well-known/oauth-protected-resource` (RFC 9728)
  advertising the resource identifier + the `authorization_servers` that
  mint tokens for it. When Paladin is itself the AS (same origin), also serve
  `/.well-known/oauth-authorization-server` (RFC 8414) — driven by config,
  advertising auth-code + refresh grants and mandatory PKCE S256 (the
  contract the deferred AS phase fulfils). When delegating to a federated
  IdP, leave the AS issuer empty and the client fetches the IdP's own
  metadata.
- **Challenge.** An unauthenticated (or invalid-token) `/mcp` request gets
  `401 + WWW-Authenticate: Bearer resource_metadata="…"` (RFC 9728 §5.1) so
  a compliant client can begin discovery. Invalid tokens carry
  `error="invalid_token"` (RFC 6750).
- **Token intake.** Accept the standard `Authorization: Bearer` header, with
  `X-Paladin-Token` kept as a fallback for existing bridge deployments
  (`mcp.BearerToken`).
- **Edge validation.** Validate the bearer at the MCP edge — signature +
  issuer + expiry, but **not** audience: an agent token targets whichever
  plane it calls (admin/data/iam), and the planes enforce audience
  downstream. This reuses the existing `auth.JWTVerifier` / `JWKSVerifier`
  via a shared `buildAgentVerifier`, which also feeds the session-subject
  enrichment — one verifier, one policy.

The capability model is untouched: OAuth authenticates the *principal*; the
fine-grained `X-Paladin-Capability` caveat authority is still operator-provisioned
and never self-minted (an agent issuing its own capability stays in
`DefaultAlwaysDeny`).

## Consequences

- Standard MCP clients can discover and authenticate against Paladin without a
  hand-pasted token, as soon as an Authorization Server exists to point at.
- The RS works with either Paladin-IAM-as-AS ([ADR-0009](0009-oauth-authorization-server.md))
  or a federated IdP — the config decides; no code change to switch.
- Edge validation turns a bad/expired token into an immediate, correct `401`
  challenge instead of a confusing downstream `Unauthenticated` on the first
  RPC. Any token the planes accept is signed by the same key, so it also
  passes the edge — no behaviour change for valid callers.
- **Deferred (the AS phase, tracked in BACKLOG):** `/authorize` with browser
  consent, `/token`, dynamic client registration (RFC 7591), the
  `oauth_clients` / `oauth_authorization_codes` / refresh-rotation storage,
  audience-binding enforcement (RFC 8707), and the consent UI. Until then,
  tokens come from `AuthService.Login` / `APITokenService.Create`.
- ~~Open decision (shared with "Phase 5b.1 — drop user-authn IAM, accept
  OIDC"): whether the AS is Paladin IAM or a federated OIDC IdP.~~ **Resolved
  2026-06-30:** the AS is **Paladin IAM** ([ADR-0009](0009-oauth-authorization-server.md)).
  Paladin is engineer-operated and owns its IAM, so no external IdP is planned
  (Phase 5b.1 withdrawn — see [ADR-0006](0006-deferred-roadmap.md)). The RS
  half is IdP-agnostic, so a federated swap stays cheap if a customer ever
  mandates SSO — but it is not on the roadmap.
