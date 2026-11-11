## release/v0.2.0 — billing answers a failing ledger by its limes code

- `BillingService` now answers `UNAVAILABLE`, worth a retry, when the charge
  ledger's store is down or timed out (`store_unavailable`, `store_timeout`),
  or returns a cursor it already returned. Before, all of these were
  `INTERNAL`.
