## release/v0.3.0 — migrate and bootstrap wait for the database

- `paladin migrate` and `paladin bootstrap` retry an unreachable database for
  up to 2 minutes before they fail, instead of failing on the first refused
  connection. Each attempt that fails is logged at WARN with its cause.
- The chart's `jobs.bootstrap.activeDeadlineSeconds` default rises from 120
  to 180, so the Job outlasts that wait. A deadline set below about 180
  seconds, for either Job, cuts the wait short.
