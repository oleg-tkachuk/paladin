## Unreleased — roles wait for Postgres at start

- A role that finds Postgres down at start retries, logging each refusal,
  for `datastores.postgres.timeouts.startup_wait` (90s by default) before it
  exits, where it exited at once. Once the database is back, roles start
  within seconds instead of after the kubelet's restart delay.
- Keep `startup_wait` inside the startup probe's window
  (`initialDelaySeconds + periodSeconds × failureThreshold`, 125s by
  default); `0` restores the single attempt.
