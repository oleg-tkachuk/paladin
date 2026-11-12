## Unreleased — the console calls the planes through the session role

- The console holds no token now (ADR-0048). The browser calls the session
  role at `/session/` on the console's own host, with the session cookie
  `__Host-paladin_session` and nothing else; the role attaches each plane's
  access token. Everyone signs in again once: the old cookie, `paladin_rt_iam`,
  is no longer read.
- The backend chart runs `deployments.session` by default, and the console
  chart routes `/session/` to `paladin-core-session`. Set
  `session.service.name` in the console chart if the backend release is named
  otherwise. The console and `/session/` must share a host, as the charts'
  ingress makes them; `config.session.console_origins` is optional and names
  further origins the console is served from.
- A state-changing call must carry an `Origin` naming that host. The session
  role trusts `X-Forwarded-Host` from the ingress for it.
- The console's `/api/auth/*`, `/api/rpc/*`, `/api/shell` and
  `/api/audit/stream` routes are gone; `/api/banner` serves the operator's
  banner. Its server now reads only `PALADIN_IAM_URL`, for the OAuth consent
  relay.
- Compose runs a Caddy front, `paladin-front`, on `localhost:3002`: it serves
  the console and routes `/session/*` to `paladin-session`, as the ingress
  does on a cluster.
