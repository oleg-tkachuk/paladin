## Unreleased — the console ships as a static export behind a Go server

- The console image runs no Node: a Go server serves the static export and
  the routes it needs (ADR-0049). The chart's values keep their names; the
  default `resources` drop to 50m/64Mi requested and 1000m/128Mi limit, and
  the `next-cache` volume is gone.
- With `backend.tls`, the relay trusts the backend's CA through
  `PALADIN_BACKEND_CA_FILE`, which the chart sets; `NODE_EXTRA_CA_CERTS` is no
  longer set. An overlay that put `NODE_TLS_REJECT_UNAUTHORIZED` or
  `NODE_OPTIONS` in `extraEnv` can drop them: nothing reads them.
- `PALADIN_BFF_MAX_CONNECTIONS` is gone; the relay carries one form at a time.
- The image's healthcheck is `paladin-console probe`; a compose file that
  checked the console with `wget` must use it, since the image has no shell.
