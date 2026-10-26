"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

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
  const budgets = useQuery({
    ...budgetAlertsQuery(BUDGET_ALERT_THRESHOLD),
    retry: false,
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
    loading: atLimit.isPending || nearLimit.isPending || budgets.isPending,
    error: failed ? errorMessage(failed) : null,
  };
}
