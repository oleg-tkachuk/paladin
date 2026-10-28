// Package delegation exchanges a caller's paladin-mcp token for the
// capability that acts for its user (ADR-0041): what the exchange reads of
// the token, and what it grants. The Authorization Server serves it at its
// token endpoint; the MCP server is its client.
package delegation
