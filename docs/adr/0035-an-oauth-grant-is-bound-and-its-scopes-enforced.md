# ADR-0035: An OAuth grant is bound to its client, and its scopes are enforced

- **Status:** Accepted — implemented 2026-10-16 (migration 082,
  `authstore.OAuthGrant`, `auth.RequireOAuthWriteScope`).
- **Related:** [ADR-0009](0009-oauth-authorization-server.md), which this
  amends; [ADR-0008](0008-mcp-oauth-resource-server.md).

- **Context.** The Authorization Server (ADR-0009) shares the console's
  refresh-token table, signer and decoder. A row recorded the user, the tenant
  and the family, and nothing about the grant it came from. Three things
  followed:
  - `/oauth/token` re-issued every rotated token for `paladin-data` with no
    scope, whatever the code exchange had issued. A session granted
    `paladin-admin` lost the admin plane at its first rotation.
  - Any client redeemed any other client's refresh token, since nothing named
    the client a token was issued to. RFC 6749 §6 requires that binding.
  - Each flow redeemed the other's tokens. A console refresh token minted
    OAuth access tokens. In the other direction, an agent's OAuth refresh
    token presented to `AuthService.RefreshToken` or `ExchangeAudience`
    minted any audience its user may hold, which turned a `paladin-data`
    grant into `paladin-admin`.

  Separately, the scopes a user consents to (`paladin.read`, `paladin.write`)
  were checked once, by the Cedar gate at `/oauth/authorize`, and then
  dropped. The access token carries the user's resource scopes (`tenant:…`,
  `bucket:…`), a different vocabulary, and no trace of the consent. An agent
  granted `paladin.read` could do everything its user could.

- **Decision.**
  1. **A refresh token records its grant.** The OAuth Authorization Server
     stores the client, the audience and the consented scopes on the refresh
     row (`oauth_client_id`, `oauth_audience`, `oauth_scopes`). The three are
     set together or not at all, and a CHECK enforces it. All three NULL is a
     console login. Deleting a client deletes its tokens.
     - `/oauth/token` redeems only a token with a grant, only for the client
       it names, and re-issues the same audience and scopes.
     - The console's `RefreshToken` and `ExchangeAudience` refuse a token
       with a grant.
     - Neither flow can redeem the other's tokens. This holds without a
       reserved value: the absence of a grant is the console's mark.
  2. **Consented scopes travel with the token and are enforced on every
     call.**
     - An access token from `/oauth/token` carries the RFC 9068 claims
       `client_id` and `scope` (space-delimited). The verifier carries them
       onto the principal.
     - `paladin.read` admits the RPCs the contract marks
       `idempotency_level = NO_SIDE_EFFECTS`. `paladin.write` admits the rest.
       An interceptor on each plane, after the principal is established,
       refuses an OAuth client without `paladin.write` every other RPC.
     - The contract is the classification, not a list beside it. Cedar
       actions could not serve: `GetCollection` and `ListCollections`
       authorize as `ManageCollection`, so a rule over actions would have
       refused reads or admitted writes. A test holds every RPC named as a
       read (`Get…`, `List…`, `Count…` and the like) to carry the mark.
     - Cedar sees the grant as `context.oauth_client_id` and
       `context.oauth_scopes` on every authorization, not only at consent,
       so a tenant policy can forbid a client anywhere.
     - Console sessions, API tokens and capabilities carry no client and are
       unaffected.
     - Capabilities stay the primitive for narrowing an agent to resources
       (ADR-0010). OAuth scopes decide only whether the agent may write.

- **Consequences.**
  - An OAuth refresh token issued before migration 082 has no grant on its
    row, so `/oauth/token` refuses it. Each OAuth client authorizes once more.
  - A grant keeps its audience across rotations. The token still has one
    audience, so an MCP session still reaches one plane. Removing that limit
    is a separate decision.
  - An agent whose grant lacks `paladin.write` is refused every write with
    `PERMISSION_DENIED`, on a platform admin's account too: the interceptor
    runs before Cedar. Clients that write must ask for `paladin.write`. The
    consent screen says what each scope allows and when a grant can change
    nothing.
  - An RPC that changes state must not be marked `NO_SIDE_EFFECTS`, or a
    read-only grant reaches it. The mark already decides whether Connect
    serves the RPC over GET, so it was not free to misuse before either.
