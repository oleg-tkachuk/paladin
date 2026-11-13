## release/v0.3.0 — platform health is read by the admin role

- The console's health page calls the admin plane's
  `SystemService.GetPlatformHealth`. The admin role reads every role's
  `/system/health.json` with `runtime.health_snapshot_token`, which it already
  holds, and lists a role it cannot read as unhealthy with the reason.
- New config: `admin.health.roles` (name and URL per role), `admin.health.timeout`
  (default `5s`) and `admin.health.ca_path`. The chart fills the roles from the
  enabled deployments, at `https` with the internal CA when `internalTLS` is on;
  set them yourself only outside the chart.
- The chart's NetworkPolicies let the admin role reach every role's health
  listener, its own included.
- The console's `/api/health/all` route is gone, and the console no longer reads
  the health token. Its chart value `healthSnapshotTokenSecret` is still
  accepted and still mounted, unused; it is removed in a later release.
