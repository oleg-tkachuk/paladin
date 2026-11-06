"use client";

import { useQuery } from "@tanstack/react-query";

import { capabilityClient } from "@/lib/connect/client";

const MS_PER_SECOND = 1000;
const NANOS_PER_MS = 1_000_000;

// useCapabilityDefaults — what Issue gives a request that leaves a setting
// out, from CapabilityService.GetDefaults: the default lifetime, in
// milliseconds, or undefined until it is known.
//
// Shown, never sent: a form left on the server's default sends no TTL and the
// server applies its own, so a read that failed cannot make a form send the
// wrong one.
export function useCapabilityDefaults(): { defaultTtlMs: number | undefined } {
  const query = useQuery({
    queryKey: ["capability", "defaults"],
    queryFn: () => capabilityClient.getDefaults({}),
    // A deployment's defaults change with its config, not while a tab is open.
    staleTime: Infinity,
  });
  const ttl = query.data?.defaultTtl;
  return {
    defaultTtlMs:
      ttl === undefined
        ? undefined
        : Number(ttl.seconds) * MS_PER_SECOND + ttl.nanos / NANOS_PER_MS,
  };
}
