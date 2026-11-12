## Unreleased — the console's session role

- `paladin serve session` is a new role (ADR-0048): it will hold the console's
  session cookie and the tokens behind it, and forward the console's calls to
  the planes. It has no database and no key; it mints each plane's token at the
  iam plane and verifies refresh tokens by `auth.jwks_url`. Nothing routes to
  it yet: the console still signs in through its own server.
- New config block `session`: `server` (`:8086`), `console_origins`,
  `upstreams` (the planes, as `mcp.upstreams` names them) and `rotate_after`
  (default `5m`). The chart's values carry it; it takes effect once a release
  deploys the role.
- Compose runs a `paladin-session` container on `:8086`.
