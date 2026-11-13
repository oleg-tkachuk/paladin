## release/v0.3.0 — the charts can run the console's session role

- The backend chart adds `deployments.session` (`serve session`, ADR-0048),
  off by default. Enabling it requires `config.session.console_origins`, the
  console's origin; the chart refuses to render without one. Its NetworkPolicy
  reaches the api, iam and admin roles and no database, and those roles admit
  it.
- The console chart adds `session.service.name` and `session.service.port`:
  once the name is set, its IngressRoute and Ingress send `session.pathPrefix`
  (`/session/`) to that Service on the console's own hosts. Point it at
  `<backend release>-session`.
- Nothing calls the role yet: the console still signs in through its own
  server.
