// Why saving a tenant budget failed, read off the server's answer, so the
// budget page can say what to do next rather than repeat the server's text.

import { Code, ConnectError } from "@connectrpc/connect";

import { limesCode } from "@/hooks/errorContract";

/**
 * The limes code of a SetTenantBudget that changed nothing because the
 * tenant's charges in flight held its budget past the store's bound (limes
 * ADR-0022). The same request may be sent again.
 */
export const LIMES_TENANT_BUDGET_BUSY = "tenant_budget_busy";

/**
 * What a failed save means for the form:
 *  - conflict: the budget moved under the edit (the OCC guard); re-read it.
 *  - busy: nothing changed, the same save may simply be sent again.
 *  - other: any other failure, shown as the server said it.
 */
export type BudgetSaveFailure = "conflict" | "busy" | "other";

export function budgetSaveFailure(err: unknown): BudgetSaveFailure {
  if (limesCode(err) === LIMES_TENANT_BUDGET_BUSY) return "busy";
  if (err instanceof ConnectError && err.code === Code.Aborted) {
    return "conflict";
  }
  return "other";
}
