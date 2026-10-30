## v41.3.0 — the admin API answers capability errors by limes code

- A capability or tenant-budget store failing under the admin API is
  `UNAVAILABLE`, worth a retry, not `INTERNAL`. The caller is told only to
  retry; the store's error is logged as the failure's `cause`.
- A limes refusal the admin API answers — a delegation wider than its
  parent, a revoked or expired parent — carries the capability reason
  (`ERROR_REASON_CAPABILITY_*`) it carries when verified.
