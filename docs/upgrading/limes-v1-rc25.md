## Unreleased — limes v1.0.0-rc.25

- pgstore v0.53.0 adds migration `031`, applied at startup: it indexes the
  holds by tenant (`CREATE INDEX CONCURRENTLY`).
- Deleting an OAuth client releases its capabilities' holds in every tenant
  in one pass, so a deletion retried after a failed release still releases
  them. Before, a retry released nothing and the holds counted against the
  budgets until they expired.
- An MCP server that exchanges an agent's token a second time, for other
  planes, is now told the token was exchanged already, still as
  `invalid_target`.
