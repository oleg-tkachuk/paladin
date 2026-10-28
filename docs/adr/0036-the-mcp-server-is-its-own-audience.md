# ADR-0036: The MCP server is its own audience and exchanges the caller's token

- **Status:** Accepted; decisions 2 and 3 superseded by
  [ADR-0041](0041-the-mcp-server-exchanges-for-a-capability.md). Implemented 2026-10-16, behind `mcp.oauth.exchange`;
  recording the actor in the audit log is deferred work (BACKLOG, "The MCP
  server is its own audience").
- **Related:** supersedes the "not audience" bullet of
  [ADR-0008](0008-mcp-oauth-resource-server.md) and the single-audience
  consequence of [ADR-0009](0009-oauth-authorization-server.md); builds on
  [ADR-0035](0035-an-oauth-grant-is-bound-and-its-scopes-enforced.md).

- **Context.** The streamable-HTTP MCP server checks a bearer's signature,
  issuer and expiry, but not its audience, and forwards it unchanged to
  whichever plane a tool calls (`withRequestCredentials`). The MCP
  authorization specification forbids both: a server accepts only tokens
  issued for it, and does not pass the token it received to another service.
  Three costs follow from Paladin doing it anyway:
  - **The agent's token works without the MCP server.** It is a
    `paladin-data` or `paladin-admin` token, so whoever holds it calls the
    plane directly, past the profile filter and the always-deny list.
  - **The MCP server accepts any plane token.** A console session's token
    opens an MCP session.
  - **A session reaches one plane.** A token has one audience, so the tools
    of the other two planes fail in that session.

  What Paladin already gets right stays: each plane authorizes the user, not
  a service account, so there is no confused deputy. The fix must keep that.

- **Decision.**
  1. **A new audience, `paladin-mcp`.** The Authorization Server issues it for
     the MCP server's resource URL (`mcp.oauth.resource_url`), as the RFC
     8707 resource indicator a compliant client sends. Today `audienceFor`
     matches a resource only when it equals an audience string, so the AS
     gains that mapping. The MCP edge requires `aud = paladin-mcp`. The
     planes already refuse it, since each requires its own audience.
  2. **The MCP server exchanges the token for each plane.** It is a
     confidential OAuth client of the AS and calls RFC 8693 token exchange
     with the caller's `paladin-mcp` token as `subject_token` and the plane as
     `audience`.
     - The exchanged token is short-lived and still the user's: subject,
       tenant, roles and the agent's OAuth grant (`client_id`, `scope`), so
       ADR-0035's `paladin.write` rule holds downstream.
     - An `act` claim names the MCP server, so audit sees who relayed the call.
     - The server caches the exchanged token per subject token and audience
       until it expires.
  3. **The AS refuses an exchange** unless the subject token's audience is
     `paladin-mcp`, the requesting client is the MCP server, and the user may
     hold the requested audience (the eligibility `ExchangeAudience` applies).
  4. **stdio is out of scope.** One process serves one operator with
     `PALADIN_MCP_TOKEN`, and nothing is relayed for anyone else.

- **Consequences.**
  - One MCP session reaches all three planes.
  - A leaked agent token is useless outside the MCP server, and a console
    token no longer opens an MCP session.
  - The legacy `X-Paladin-Token` bridge path, which carries plane tokens,
    ends for HTTP. Every streamable-HTTP client authorizes through OAuth.
  - The AS gains a grant type, and the MCP server a client secret
    provisioned like any other. The first call to each plane costs one more
    round trip per token lifetime.
