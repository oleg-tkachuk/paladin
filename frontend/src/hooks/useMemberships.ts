"use client";

import { useCallback, useState } from "react";

import { useAuth } from "@/context/AuthContext";
import { authClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";

/** One tenant the signed-in subject belongs to (AuthService.Membership). */
export type Membership = {
  tenantId: string;
  tenantSlug: string;
  roles: string[];
  disabled: boolean;
  current: boolean;
};

/**
 * useMemberships fetches the caller's tenant memberships
 * (AuthService.ListMyMemberships, through the session role) for the tenant
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
      const res = await authClient.listMyMemberships({});
      setMemberships(
        res.memberships.map((m) => ({
          tenantId: m.tenantId,
          tenantSlug: m.tenantSlug,
          roles: [...m.roles],
          disabled: m.disabled,
          current: m.current,
        })),
      );
    } catch (e) {
      setError(normalizeError(e).message);
      setMemberships([]);
    } finally {
      setLoading(false);
    }
  }, [status]);

  return { memberships, loading, error, load };
}
