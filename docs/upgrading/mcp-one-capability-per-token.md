## v41.2.0 — the MCP exchange issues one capability per token

- **A paladin-mcp token is exchanged for one capability**, whichever replica
  exchanges it, and that capability lives as long as the token. Revoking it
  now also refuses the token's later exchanges (`invalid_grant`) until the
  agent's client fetches a new token, where before the next exchange issued
  a fresh capability. Nothing to configure.
