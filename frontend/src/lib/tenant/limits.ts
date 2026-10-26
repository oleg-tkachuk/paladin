// Whether a tenant is near or at its limits, from the two platform-wide
// reads that answer it: the tenants with quota rows at or near a cap
// (SystemService.ListPlatformStatsTenants) and the tenants whose budget
// utilisation crossed the alert threshold (TenantBudgetService.Summarize).
// Pure, so every page that tells a tenant's limits reads them one way.

import type { TenantBudget } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { budgetLimit } from "@/lib/format/budgetCeiling";
import { moneyToNanos } from "@/lib/format/money";
import { StatusTone } from "@/lib/ui/status";

/**
 * The utilisation percent at which a tenant counts as near its budget. 80 %
 * is the platform-wide default; operators escalate to /billing or the
 * tenant's Budget tab for fine-grained action.
 */
export const BUDGET_ALERT_THRESHOLD = 80;

/** Where a tenant's quotas stand, from the quota signal it is listed under. */
const QuotaSignal = {
  /** A quota row at or over its cap. */
  AtLimit: "at-limit",
  /** A quota row near its cap and none at one. */
  NearLimit: "near-limit",
} as const;
type QuotaSignal = (typeof QuotaSignal)[keyof typeof QuotaSignal];

/** What the platform reads say about one tenant's limits. */
export interface TenantLimitSignals {
  quota?: QuotaSignal;
  /** The tenant's budget, present when its utilisation crossed the threshold. */
  budget?: TenantBudget;
}

/** A limit state a tenant leads with, and the tone it is shown in. */
interface TenantLimitState {
  label: string;
  tone: StatusTone;
}

/** The limit states, most urgent first: tenantLimitState's precedence. */
const LIMIT_STATE = {
  OverBudget: { label: "Over budget", tone: StatusTone.Danger },
  AtQuota: { label: "At quota", tone: StatusTone.Danger },
  NearQuota: { label: "Near quota", tone: StatusTone.Warning },
  NearBudget: { label: "Near budget", tone: StatusTone.Warning },
} as const satisfies Record<string, TenantLimitState>;

/**
 * Whether the budget's spend has reached its cap, compared in exact nanos:
 * utilisation_pct is capped at 100 for display, so it cannot tell a spend
 * past the cap from one at it, and a float is not compared. A cap of zero
 * allows no spend, so it is reached at once; an unlimited budget never is.
 */
function budgetOver(budget: TenantBudget | undefined): boolean {
  const cap = budgetLimit(budget?.budgetCeiling);
  if (!cap) return false;
  return moneyToNanos(budget?.spent) >= moneyToNanos(cap);
}

/**
 * The one limit state a tenant leads with, or undefined when the reads say
 * nothing about it. A reached cap (danger) comes before a near one
 * (warning); a reached budget before a reached quota, since spend past the
 * cap is already refused for every capability the tenant runs.
 */
export function tenantLimitState(
  signals: TenantLimitSignals | undefined,
): TenantLimitState | undefined {
  if (!signals) return undefined;
  if (signals.budget && budgetOver(signals.budget))
    return LIMIT_STATE.OverBudget;
  if (signals.quota === QuotaSignal.AtLimit) return LIMIT_STATE.AtQuota;
  if (signals.quota === QuotaSignal.NearLimit) return LIMIT_STATE.NearQuota;
  if (signals.budget) return LIMIT_STATE.NearBudget;
  return undefined;
}

/**
 * The reads joined per tenant id. A tenant listed under both quota signals
 * is at its limit; Summarize may return a tenant on two pages, and the last
 * row read stands.
 */
export function limitSignalsByTenant(reads: {
  quotaAtLimit: readonly string[];
  quotaNearLimit: readonly string[];
  budgets: readonly { tenantId: string; budget?: TenantBudget }[];
}): Map<string, TenantLimitSignals> {
  const out = new Map<string, TenantLimitSignals>();
  const at = (id: string) => {
    const seen = out.get(id);
    if (seen) return seen;
    const fresh: TenantLimitSignals = {};
    out.set(id, fresh);
    return fresh;
  };
  for (const id of reads.quotaNearLimit) at(id).quota = QuotaSignal.NearLimit;
  for (const id of reads.quotaAtLimit) at(id).quota = QuotaSignal.AtLimit;
  for (const row of reads.budgets) {
    if (row.budget) at(row.tenantId).budget = row.budget;
  }
  return out;
}
