"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { canManageTenantBudgets } from "@/constants/roles";
import { useAuth } from "@/context/AuthContext";
import { errorMessage } from "@/hooks/errorContract";
import {
  budgetAlertsQuery,
  QUOTA_LIMIT_SIGNALS,
  quotaSignalTenantsQuery,
} from "@/lib/queries/tenantLimits";
import {
  BUDGET_ALERT_THRESHOLD,
  limitSignalsByTenant,
  type TenantLimitSignals,
} from "@/lib/tenant/limits";

const NONE: readonly never[] = [];

/**
 * useTenantLimitSignals — which tenants are near or at a quota cap or their
 * budget, per tenant id, from the platform-wide reads (lib/queries/
 * tenantLimits). Each read stands alone: one that fails or has not landed
 * yet adds nothing, so a tenant shows without the state it would tell
 * rather than the list breaking. Never retried: the reads are the platform
 * admin's, and a refusal will not change on a second ask.
 */
export function useTenantLimitSignals(): {
  signals: ReadonlyMap<string, TenantLimitSignals>;
  loading: boolean;
  error: string | null;
} {
  const atLimit = useQuery({
    ...quotaSignalTenantsQuery(QUOTA_LIMIT_SIGNALS.atLimit),
    retry: false,
  });
  const nearLimit = useQuery({
    ...quotaSignalTenantsQuery(QUOTA_LIMIT_SIGNALS.nearLimit),
    retry: false,
  });
  // Every tenant's budget is the platform admin's to read; for anyone else
  // the summary is not asked for, and adds nothing.
  const { user } = useAuth();
  const budgetsReadable = canManageTenantBudgets(user?.roles ?? []);
  const budgets = useQuery({
    ...budgetAlertsQuery(BUDGET_ALERT_THRESHOLD),
    retry: false,
    enabled: budgetsReadable,
  });

  const signals = useMemo(
    () =>
      limitSignalsByTenant({
        quotaAtLimit: atLimit.data ?? NONE,
        quotaNearLimit: nearLimit.data ?? NONE,
        budgets: budgets.data ?? NONE,
      }),
    [atLimit.data, nearLimit.data, budgets.data],
  );
  const failed = atLimit.error ?? nearLimit.error ?? budgets.error;
  return {
    signals,
    loading:
      atLimit.isPending ||
      nearLimit.isPending ||
      (budgetsReadable && budgets.isPending),
    error: failed ? errorMessage(failed) : null,
  };
}
