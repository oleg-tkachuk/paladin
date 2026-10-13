"use client";

import { useQuery } from "@tanstack/react-query";

import { billingClient } from "@/lib/connect/client";

// usePlatformCurrency — the platform's currency (capability.currency), from
// BillingService.GetBillingSettings: what a budget or capability that names
// no currency is in.
//
// Shown, never sent. A form that the operator leaves on the platform's
// currency sends no unit and the server applies its own setting, so a read
// that failed or has not landed yet cannot make a form send the wrong one —
// the reason it was the console's hard-coded XXX before is exactly that it
// could disagree with the server.
export function usePlatformCurrency(): string | undefined {
  const query = useQuery({
    queryKey: ["billing", "settings"],
    queryFn: () => billingClient.getBillingSettings({}),
    // A deployment's currency changes with its config, not while a tab is open.
    staleTime: Infinity,
  });
  return query.data?.currencyCode || undefined;
}
