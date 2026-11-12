## Unreleased — the IAM plane runs as its own role

- `paladin serve iam` serves the IAM listener (`:8085`): login and the token
  RPCs, users, the OAuth authorization server and the session JWKS. It is the
  one role that loads `auth.session_key_path` (ADR-0047). `serve api` now
  serves the data plane alone and verifies sessions by `auth.jwks_url`, so it
  needs that URL and no longer opens `:8085`.
- The chart adds `deployments.iam` with its own Service, NetworkPolicy and
  Role; `auth.sessionKey.roles` defaults to `[iam]`, and `config.auth.jwks_url`
  and `config.mcp.upstreams.iam_url` point at `<release>-iam:8085`. The
  IngressRoute and Ingress send the IAM procedures, `/oauth/`, the AS metadata
  and `/.well-known/jwks.json` to it. Anything dialling `<release>-api:8085`
  must dial `<release>-iam:8085` instead; the console chart does so itself.
- With `internalTLS` on, the certificate's SANs must also name
  `<release>-iam`, or every role's JWKS fetch fails its TLS check.
- Overrides set under `deployments.api` for probes, resources or replicas are
  not inherited: set them under `deployments.iam` as well.
- The OAuth AS metadata's `jwks_uri` is derived from its issuer;
  `mcp.oauth.authorization_server.jwks_uri` is removed from the config, and a
  config still setting it is refused at load.
- `auth.signingKey`, accepted unused since sessions became ES256, is removed
  from the chart, and an overlay still setting it fails schema validation.
- Compose runs a `paladin-iam` container; the console's health page lists
  `iam` beside `api` (add `iam` to `backend.healthRoles` if you set it).
