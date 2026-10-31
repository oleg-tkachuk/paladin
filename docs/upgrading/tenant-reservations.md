## sdk/go/v0.129.0, sdk/python/v0.130.0 — a tenant's holds on its budget

- `TenantBudget.reserved` is what the tenant's open holds keep from the
  budget, counted against the cap as spend is.
- `TenantBudgetService.ListReservations` lists those holds, soonest to
  expire first, a page at a time. It is a read, allowed to whoever may read
  the tenant's budget.
