# ADR-0047: Session tokens are signed with a key only the IAM role holds

- **Status:** Accepted 2026-11-11.
- **Supersedes in part:** [ADR-0006](0006-deferred-roadmap.md) (its
  2026-06-30 amendment's "local users + HS256") and
  [ADR-0041](0041-the-mcp-server-exchanges-for-a-capability.md) (IAM tokens
  "stay HS256").
- **Related:** [ADR-0009](0009-oauth-authorization-server.md) (the OAuth
  authorization server), [ADR-0048](0048-the-console-holds-no-token.md)
  (the console's session, which builds on this one).

## Context

Paladin's own IAM signs every user session — access and refresh tokens, and
the OAuth authorization server's tokens — with one HS256 secret,
`auth.signing_key` (`internal/auth/issuer`). A symmetric key verifies and
mints alike, so everything that can check a session can forge one. And
everything can read it:

- `BuildSharedDeps` refuses to start without it, so every role that opens
  the database — api, admin, worker, dispatcher, ingest, embedded mcp —
  loads it, though only `serve api` mints (`wire.ProvideIssuer`, login,
  refresh, the OAuth token endpoint).
- The chart binds one secret-reader Role, whose names include the key's
  Secret, to every Deployment's service account and to the migrate and
  bootstrap jobs. A worker or the MCP bridge compromised is an operator
  session minted.
- The same secret seeds the API-token HMAC pepper when
  `api_token.hmac_key` is unset (`build_api_token.go`), so it guards the
  M2M tokens too.
- The login, refresh and OAuth endpoints run in the `api` pods beside the
  data plane: the process that parses uploads holds the key, and password
  hashing shares its CPU.
- The issuer's comment describes a rotation window (`Active`, `Previous`,
  `GracePeriod`) that does not exist; there is one key and no rotation.

Moving the IAM endpoints to their own role would not fix the first point:
while the key is symmetric, every verifier still holds a minting key. The
key has to become asymmetric first, and the private half held by the
issuer alone.

## Decision

1. **Sessions are signed ES256 (ECDSA P-256), and HS256 is removed for user
   tokens.** Access and refresh tokens, and the OAuth AS's, are signed with
   one private key and verified with its public key. ES256 rather than
   EdDSA because both verifiers already handle it (`JWTVerifier` by key
   type, `JWKSVerifier` keeps RSA and EC keys and drops OKP); a token
   signed HS256 — including one HMAC'd with the public key as the secret —
   is refused.
2. **Keys carry a `kid`, and the issuer publishes a key set.** The issuer
   signs with the active key and publishes its public key with any
   previous ones at the IAM plane's `/.well-known/jwks.json`, apart from
   the admin plane's capability JWKS. A previous key stays published for
   the refresh-token lifetime after a rotation, so no session dies of it.
   The OAuth AS metadata's `jwks_uri` names that endpoint instead of a
   configured string.
3. **The private key is in a Secret only the issuing role can read.** The
   chart generates it (`genPrivateKey "ecdsa"`, kept across syncs) in a
   Secret of its own, under a Role bound to that role's service account
   alone — from the first release that has the key, not after the role
   split.
4. **Verifiers hold public keys only.** The issuing process verifies with
   its own key set in memory, and so does the embedded all-in-one mode;
   every other role fetches the IAM JWKS (`auth.jwks_url`), with the
   existing cache, background refresh and rate-limited refresh on an
   unknown `kid`. With no key cached, a session is refused
   `UNAUTHENTICATED` and the failure is logged; a failed refresh keeps the
   cached keys. Readiness does not wait on IAM.
5. **Only the issuer requires the key at startup.** `BuildSharedDeps` stops
   requiring `auth.signing_key`; a role that mints requires the private key,
   a role that verifies requires a key source (local or `jwks_url`).
6. **The API-token pepper is its own secret.** `api_token.hmac_key` is
   required wherever API tokens are verified or issued, and no longer
   derived from the session key; the chart generates it. Existing API tokens
   stop verifying once: Helm cannot reproduce the derivation, and the dev
   cluster's tokens are re-issued.
7. **IAM runs as its own role, `serve iam`.** It serves the IAM plane, the
   OAuth authorization server and the session JWKS, with its own
   Deployment, Service, NetworkPolicy and scaling; `serve api` serves the
   data plane only. The embedded mode keeps hosting both for development.
8. **Every role's service account reads only its own Secrets.** The shared
   secret-reader Role is replaced by one per role, rendered from what each
   role is configured with.

Each step deploys alone, as every green merge reaches the dev cluster, and
each is a hard cut there: the switch to ES256 ends every session once.

## Consequences

- A compromised worker, dispatcher, MCP bridge or admin pod can no longer
  mint a session. Until `serve iam` exists, the `api` pods still hold the
  private key; that is the residual risk the role split closes.
- Admin, MCP and the OAuth token exchange depend on the IAM JWKS being
  reachable once per key; while IAM is down with nothing cached, sessions
  are refused rather than trusted.
- Rotating the session key is a key added to the set and an issuer restart,
  with no verifier redeployed.
- Development tooling signs with a key file instead of a shared string:
  `auth:mint-token` takes `JWT_ALG=ES256` and the PEM as `JWT_SECRET`, the
  compose stack generates a throwaway key at start, and tests mint with an
  ephemeral key.
- A future external IdP stays possible — verifiers already take a JWKS —
  and stays out of scope ([ADR-0006](0006-deferred-roadmap.md)'s
  amendment is unchanged on that point).
