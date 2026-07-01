"use client";

import { useCallback, useState } from "react";

import { useAuth } from "@/context/AuthContext";

/** One tenant the signed-in subject belongs to (AuthService.Membership). */
export type Membership = {
  tenantId: string;
  tenantSlug: string;
  roles: string[];
  disabled: boolean;
  current: boolean;
};

/**
 * useMemberships fetches the caller's tenant memberships from the BFF
 * (/api/auth/memberships → AuthService.ListMyMemberships) for the tenant
 * switcher. `load` is lazy — call it when the switcher opens rather than on
 * mount, so an operator who never switches pays nothing. The list is always
 * refreshed on the next `load` (the picker calls it on open), so it never
 * needs to be invalidated on a switch.
 */
export function useMemberships() {
  const { status } = useAuth();
  const [memberships, setMemberships] = useState<Membership[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (status !== "authenticated") return;
    setLoading(true);
    setError(null);
    try {
      const res = await fetch("/api/auth/memberships", {
        credentials: "same-origin",
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}) as { error?: string });
        throw new Error(body?.error || `memberships failed (${res.status})`);
      }
      const body = (await res.json()) as { memberships: Membership[] };
      setMemberships(body.memberships ?? []);
    } catch (e) {
      setError((e as Error).message);
      setMemberships([]);
    } finally {
      setLoading(false);
    }
  }, [status]);

  return { memberships, loading, error, load };
}
