## Unreleased — `GetBridgeStatus` answers a verdict, not a transport error

When no MCP replica answered, `error` is one of `the MCP server is
unreachable`, `the MCP server timed out`, `the MCP server answered HTTP <n>` or
`the MCP server's answer could not be read`. It used to be the HTTP client's
error, naming the replica's URL. The full error is in the admin plane's
`mcp bridge unreachable` log line. Each upstream's `error` is unchanged.
