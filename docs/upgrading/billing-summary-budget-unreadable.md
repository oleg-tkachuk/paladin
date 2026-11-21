## Unreleased — a billing summary fails when the tenant's budget cannot be read

- `GetTenantSummary` now fails when the tenant budget store cannot be read:
  `UNAVAILABLE` when the store is down or timed out, `INTERNAL` otherwise.
  Before, it answered the summary with no budget ceiling, which reads as a
  tenant with no cap configured. A tenant with no budget still gets a summary
  without a ceiling.
