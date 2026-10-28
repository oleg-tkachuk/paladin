## Unreleased — the MCP server acts through a capability that acts for its user

- **The token-exchange grant issues a capability** (ADR-0041). `/oauth/token`
  with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` answers
  with one limes capability for every plane the user may hold
  (`issued_token_type` `https://github.com/oleg-tkachuk/limes/exchange#capability`),
  no longer with an IAM access token per plane. `audience` names planes by
  their labels, `data`, `admin` and `iam`, and may repeat. The MCP server
  is its only client and moves with it; nothing to do there.
- **The grant needs `capability.enabled`.** A seed client with
  `token_exchange: true` and the capability subsystem off refuses to start.
- **An agent session is revocable on its own.** Each MCP caller's calls
  carry a capability issued under `<capability.issuer_name>/mcp`; revoking
  it stops the session without signing the user out. The audit log's
  `relayed_by` still names `paladin-mcp`.
- **A user's roles and status apply at the next call.** The planes read
  the user behind a delegated capability on every call, so a role removed
  or a user disabled stops their agents at once.
- **A caller that sends its own capability keeps it.** The MCP edge then
  forwards that capability alone; it no longer forwards the caller's
  `paladin-mcp` token, which no plane accepts.
