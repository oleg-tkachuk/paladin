# ADR-0035: An OAuth grant is bound to its client, and its scopes are enforced

- **Status:** Accepted. Decision 1 implemented 2026-10-16 (migration 082,
  `authstore.OAuthGrant`); decision 2 follows.
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
     request.**
     - An access token from `/oauth/token` carries the RFC 9068 claims
       `client_id` and `scope` (space-delimited).
     - The verifier carries them onto the principal, and Cedar sees them as
       `context.oauth_client_id` and `context.oauth_scopes` on every
       authorization, not only at consent.
     - A built-in policy gives the two scopes their meaning. A principal
       acting for an OAuth client performs an action that changes state only
       when its grant holds `paladin.write`. `paladin.read` is enough for the
       actions that only read.
     - Which actions read is a list in Go, beside the actions themselves. A
       test holds every declared action to be classified, so a new action
       cannot ship unclassified.
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
  - After decision 2, an agent whose grant lacks `paladin.write` is refused
    every write, including on a platform admin's account. The refusal is a
    `forbid`, and a `forbid` beats the built-in `platform.admin` permit.
    Clients that write must ask for `paladin.write`, and the consent screen
    names it.
