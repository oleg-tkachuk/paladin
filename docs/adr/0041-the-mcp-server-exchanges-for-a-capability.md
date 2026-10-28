# ADR-0041: The MCP server exchanges the caller's token for a capability that acts for the user

- **Status:** Accepted. Implementation in progress.
- **Related:** supersedes decisions 2 and 3 of
  [ADR-0036](0036-the-mcp-server-is-its-own-audience.md) (its `paladin-mcp`
  audience stays); amends [ADR-0010](0010-capability-as-establishing-credential.md)
  (a capability carries no role) for one issuer; keeps
  [ADR-0035](0035-an-oauth-grant-is-bound-and-its-scopes-enforced.md)'s
  `paladin.write` rule, now carried by the capability; follows limes'
  ADR-0003 (bridge OAuth to capabilities by token exchange).

- **Context.** Under ADR-0036 the MCP server trades the caller's
  `paladin-mcp` token for an IAM access token per plane, over HTTP, at
  Paladin's own Authorization Server, as a confidential client. The token
  it gets back is the user's and nothing narrower:
  - no budget, request ceiling or source range bounds an agent session;
  - the only way to stop one is to revoke the user's refresh family.

  limes' `exchange` module serves the same grant and issues a capability,
  which carries all three. A capability alone does not reach what the bridge
  needs, though. Of the 81 tools in `DefaultCatalog`, 49 call the admin plane
  and 2 the IAM plane, whose Cedar built-ins permit by role. A capability has
  no role (ADR-0010), and it names one tenant, so the platform tools
  (backends, every tenant, system config, platform operations) could not
  ride one at all.

- **Decision.**
  1. **The exchange is limes', at the Authorization Server.** The token
     endpoint's RFC 8693 grant is served by limes' `exchange.Handler` over
     an `exchange.Exchanger` with the delegation issuer below. The MCP
     server stays its confidential client, configured by
     `mcp.oauth.exchange` as before: the bridge runs in a pod of its own
     with no database and faces the internet, so it holds neither the
     capability key nor the user store. The `SubjectVerifier` is Paladin's
     own `auth.JWTVerifier`, which accepts only `aud = paladin-mcp`;
     Paladin's IAM tokens stay HS256 `JWT`, and the verifier runs limes'
     `exchangetest` contract suite. The policy and the verifier live in
     `internal/auth/delegation`, which the Authorization Server imports.
  2. **The capability acts for the user.** The exchange policy issues one
     capability per caller's token, cached until it nears expiry:
     - `Subject`: the agent, named by its OAuth client (`MCPClient`), in
       the user's tenant; `OnBehalfOf`: the user (`sub`, a UUID). Both are
       signed into the capability;
     - `Issuer`: an issuer of its own, `<issuer_name>/mcp`, signing with the
       capability key under a kid of its own. The planes trust it, bound to
       that kid alone, so its `iss` marks a delegated capability: signed,
       and written by no other issuer. `IssuedBy` records the `paladin-mcp` service;
     - `Audience`: every plane the user may hold
       (`apiutil.AudienceEligible`); none is `invalid_target`;
     - lifetime: no later than the subject token, and at most the plane
       token lifetime it replaces;
     - caveats: the operations below, and no `share`, so it cannot be
       delegated; no request or budget ceiling, the user's authority bounding
       it as it bounds their token; no `AllowTaintedRead`, since the bridge is an agent and a
       tainted object is closed to agents.
  3. **The plane authorizes the user, narrowed by the caveats.** A
     delegated capability establishes the principal ADR-0036's exchanged
     token did: kind `user`, the user's subject and tenant, the user's
     current roles read from IAM on every call, the OAuth client and
     scopes, and `paladin-mcp` as the actor. Cedar decides on it as for the
     user's own token. A capability from any other issuer keeps ADR-0010
     unchanged: no roles.
  4. **`paladin.write` is a caveat.** Consent to `paladin.read` issues the
     operations `paladin:read`, `get`, `list` and `object:presign`; consent to
     `paladin.write` adds `paladin:write`, `put`, `delete`, `manage` and
     `object:tag`. The principal
     carries the scope the operations name, so the write interceptor and
     Cedar's `context.oauth_scopes` read it as they do today. One table in
     one package maps each scope to its operations.
  5. **ADR-0036's plane tokens go.** The grant no longer mints an IAM
     access token per plane with an `act` claim; the MCP server asks once
     for every plane and gets one capability. The `paladin-mcp` audience,
     the edge's audience check, the protected resource metadata and the
     exchange client's configuration stay.
  6. **The edge swaps the token for the capability.** After the session is
     bound to the caller's token, the MCP edge replaces it with the
     capability, so the bridge's calls carry nothing else. A caller that
     sends a capability of its own keeps it, and the token is dropped.

- **Consequences.**
  - An agent session is bounded and revocable on its own: revoking its
    capability stops it without logging the user out.
  - A tainted object is closed to the bridge, as to every agent's
    capability without `AllowTaintedRead`; through ADR-0036's plane tokens
    it was not. Reading one stays the user's, by their own session.
  - All 81 tools keep working, platform ones included, and a role removed
    from the user, or the user disabled, takes effect at the next call rather
    than at the next token refresh.
  - The capability key stays where it was, with the planes and the
    Authorization Server; the MCP server gains none. A refused exchange is
    answered at the MCP edge as RFC 6750 says, before a tool runs.
  - No configuration changes: `mcp.oauth.exchange` and the seed client's
    `token_exchange` keep their meaning, and the grant needs
    `capability.enabled`.
  - The audit log's `relayed_by` still names `paladin-mcp`, now from the
    capability rather than an `act` claim, and each relayed call's entry
    carries the capability's ID.
