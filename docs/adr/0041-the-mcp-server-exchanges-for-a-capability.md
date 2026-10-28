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
  - the only way to stop one is to revoke the user's refresh family;
  - the server holds a client secret, and the AS keeps a grant type for one
    client.

  limes' `exchange` module does this in process and issues a capability,
  which carries all three. A capability alone does not reach what the bridge
  needs, though. Of the 81 tools in `DefaultCatalog`, 49 call the admin plane
  and 2 the IAM plane, whose Cedar built-ins permit by role. A capability has
  no role (ADR-0010), and it names one tenant, so the platform tools
  (backends, every tenant, system config, platform operations) could not
  ride one at all.

- **Decision.**
  1. **The exchange is limes', in process.** The MCP server runs an
     `exchange.Exchanger` with the capability issuer the admin plane uses.
     Its `SubjectVerifier` is Paladin's own `auth.JWTVerifier`, which
     accepts only `aud = paladin-mcp`. Paladin's IAM tokens stay HS256
     `JWT`, and the verifier runs limes' `exchangetest` contract suite.
  2. **The capability acts for the user.** The exchange policy issues, per
     plane:
     - `Subject`: the agent, named by its OAuth client (`MCPClient`), in
       the user's tenant; `OnBehalfOf`: the user (`sub`, a UUID). Both are
       signed into the capability;
     - `Issuer`: an issuer of its own, `<issuer_name>/mcp`, signing with a
       key derived (HKDF) from the capability signing key under a kid of its
       own. The planes trust it, bound to that kid alone, so its `iss` is the
       mark of a delegated capability: signed, and written by no other
       issuer. `IssuedBy` records the `paladin-mcp` service;
     - `Audience`: the plane, refused (`invalid_target`) when the user may
       not hold it (`apiutil.AudienceEligible`);
     - lifetime: no later than the subject token, and at most the plane
       token lifetime it replaces;
     - caveats: the operations below, and no `share`, so it cannot be
       delegated.
  3. **The plane authorizes the user, narrowed by the caveats.** A
     delegated capability establishes the principal ADR-0036's exchanged
     token did: kind `user`, the user's subject and tenant, the user's
     current roles read from IAM (cached briefly), the OAuth client and
     scopes, and `paladin-mcp` as the actor. Cedar decides on it as for the
     user's own token. A capability from any other issuer keeps ADR-0010
     unchanged: no roles.
  4. **`paladin.write` is a caveat.** Consent to `paladin.read` issues the
     operations `paladin:read`, `get` and `list`; consent to `paladin.write`
     adds `paladin:write`, `put`, `delete` and `object:tag`. The principal
     carries the scope the operations name, so the write interceptor and
     Cedar's `context.oauth_scopes` read it as they do today. One table in
     one package maps each scope to its operations.
  5. **ADR-0036's exchange goes.** The AS's token-exchange grant, the MCP
     client secret and `mcp.oauth.exchange` are removed. The `paladin-mcp`
     audience, the edge's audience check and the protected resource metadata
     stay.

- **Consequences.**
  - An agent session is bounded and revocable on its own: revoking its
    capability stops it without logging the user out.
  - All 81 tools keep working, platform ones included, and a role removed
    from the user takes effect within the role cache's lifetime rather than
    at the next token refresh.
  - The MCP server signs capabilities, so it holds the capability signing
    key. KMS-wrapping that key stays its own BACKLOG item.
  - A configuration naming `mcp.oauth.exchange` fails the strict loader;
    the chart and local-iac drop it with the release that ships this.
  - The audit log's `relayed_by` reads the capability's `IssuedBy`, and the
    log gains the capability ID for each relayed call.
