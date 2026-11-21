## Unreleased — a billing summary says when the tenant's budget cannot be read

- `GetTenantSummaryResponse.budget_ceiling` has a new arm,
  `budget_unreadable` (an empty `BudgetUnreadable` message): the tenant budget
  store could not be read, so whether the tenant has a cap is unknown. The
  spend, refunds, charge count and top lists are still the ledger's, and
  `GetTenantSummary` answers them rather than failing. Before, it answered such
  a summary with no budget ceiling, which reads as a tenant with no cap
  configured. A tenant with no budget still gets a summary with no arm set.
- A client that branches on `budget_ceiling` should handle the new arm. A
  client built from stubs older than this release sees it as an unset oneof —
  no budget — so regenerate before relying on the distinction.
- A ledger that cannot be read still fails the summary, as before.
  `GetTenantTimeSeries`, which reads the budget only for a unit to fall back
  on, is unchanged: it logs the failure and answers in the platform's
  currency.
