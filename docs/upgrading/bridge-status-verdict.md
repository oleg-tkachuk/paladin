## Unreleased — `GetBridgeStatus` answers a verdict, not a transport error

When no MCP replica answered, `error` is one of `the MCP server is
unreachable`, `the MCP server timed out`, `the MCP server answered HTTP <n>` or
`the MCP server's answer could not be read`. It used to be the HTTP client's
error, naming the replica's URL. The full error is in the admin plane's
`mcp bridge unreachable` log line.

Each upstream's `error` is a verdict too: `the plane is unreachable`, `the
plane timed out`, `the plane's certificate was refused`, `the TLS handshake
with the plane failed` (the plane ended it, as when it refuses the MCP
server's client certificate) or `the plane answered HTTP <n>`. It used to be
the MCP server's own probe error. The full error, x509 detail included, is in
the MCP server's `mcp upstream probe failed` log line. The console names the
verdict on the unreachable plane's badge.
