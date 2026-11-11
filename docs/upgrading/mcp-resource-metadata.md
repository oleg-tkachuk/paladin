## release/v0.2.0 — the MCP edge's resource metadata comes from limes

- With `mcp.oauth.enabled`, `mcp.oauth.resource_url` and every
  `mcp.oauth.authorization_servers` entry must be https URLs, and at least one
  authorization server must be named (RFC 9728 §2). Otherwise the server
  refuses to boot. Both are identifiers: the MCP listener may stay plain http
  behind a proxy or ingress that terminates TLS. Compose now names
  `https://localhost:8095/mcp` and `https://localhost:8085`.
- The protected-resource document is now also served where RFC 9728 §3.1 puts
  it for a resource with a path, `/.well-known/oauth-protected-resource/mcp`,
  and the `WWW-Authenticate` challenge points there. The bare
  `/.well-known/oauth-protected-resource` still answers.
- The document is cacheable for an hour (`Cache-Control: max-age=3600`),
  where it was five minutes.
- In a challenge carrying an error, `resource_metadata` now comes after
  `error` and `error_description`. A quote in a description is escaped.
