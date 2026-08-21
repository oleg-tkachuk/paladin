"use client";

import { useCallback } from "react";
import { useQuery } from "@tanstack/react-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { objectClient } from "@/lib/connect/client";
import type { ObjectLockState } from "@/gen/paladin/data/v1/types_pb";
import { useAuth } from "@/context/AuthContext";
import { useRefreshSignal, useBumpRefresh } from "@/context/RefreshContext";
import { useNotification } from "@/components/ui/Notification";
import { normalizeError } from "@/lib/connect/error";

/**
 * useObjectLock — the object-lock surface for one object (ADR-0013).
 *
 * Retention and legal hold are separate calls because their rules differ, not
 * because the API is fussy: a hold can be lifted by whoever may set it, while
 * a retention window can only ever be extended — and under COMPLIANCE, not
 * even by a platform admin. The UI has to make that asymmetry visible, so the
 * hook keeps them apart rather than offering one "save lock" that sometimes
 * silently refuses half of what was asked.
 *
 * `bypassGovernance` is passed through untouched. The server gates it on a
 * role and ignores it entirely for COMPLIANCE; sending it optimistically
 * would be wrong, so the caller decides.
 */

export interface UseObjectLockResult {
  lock: ObjectLockState | null;
  isLoading: boolean;
  error: string | null;
  setRetention: (args: {
    mode: "GOVERNANCE" | "COMPLIANCE";
    retainUntil: Date;
    bypassGovernance?: boolean;
  }) => Promise<void>;
  setLegalHold: (hold: boolean) => Promise<void>;
  refresh: () => Promise<void>;
}

export function useObjectLock(
  objectName: string | undefined,
): UseObjectLockResult {
  const { user } = useAuth();
  const { showNotification } = useNotification();
  const refreshSignal = useRefreshSignal("objects");
  const bumpRefresh = useBumpRefresh();

  const enabled = Boolean(objectName) && Boolean(user?.tenantId);

  const query = useQuery({
    queryKey: ["objectLock", objectName, refreshSignal],
    enabled,
    queryFn: async () => {
      const res = await objectClient.getObjectLock({ name: objectName! });
      return res;
    },
  });

  const refresh = useCallback(async () => {
    await query.refetch();
  }, [query]);

  const setRetention = useCallback(
    async ({
      mode,
      retainUntil,
      bypassGovernance = false,
    }: {
      mode: "GOVERNANCE" | "COMPLIANCE";
      retainUntil: Date;
      bypassGovernance?: boolean;
    }) => {
      if (!objectName) return;
      try {
        await objectClient.setObjectRetention({
          name: objectName,
          mode,
          retainUntil: timestampFromDate(retainUntil),
          bypassGovernanceRetention: bypassGovernance,
        });
        await refresh();
        bumpRefresh("objects");
        showNotification({
          type: "success",
          title: "Retention Applied",
          message:
            mode === "COMPLIANCE"
              ? "This version is now retained under COMPLIANCE and cannot be released early."
              : "Retention window updated.",
        });
      } catch (err: unknown) {
        showNotification({
          type: "error",
          title: "Retention Not Applied",
          message: normalizeError(err).message,
        });
        throw err;
      }
    },
    [objectName, refresh, bumpRefresh, showNotification],
  );

  const setLegalHold = useCallback(
    async (hold: boolean) => {
      if (!objectName) return;
      try {
        await objectClient.setObjectLegalHold({
          name: objectName,
          legalHold: hold,
        });
        await refresh();
        bumpRefresh("objects");
        showNotification({
          type: "success",
          title: hold ? "Legal Hold Placed" : "Legal Hold Released",
          message: hold
            ? "This version cannot be deleted while the hold is in place."
            : "The hold has been lifted. Any retention window still applies.",
        });
      } catch (err: unknown) {
        showNotification({
          type: "error",
          title: hold ? "Could Not Place Hold" : "Could Not Release Hold",
          message: normalizeError(err).message,
        });
        throw err;
      }
    },
    [objectName, refresh, bumpRefresh, showNotification],
  );

  return {
    lock: query.data ?? null,
    isLoading: query.isLoading,
    error: query.error ? normalizeError(query.error).message : null,
    setRetention,
    setLegalHold,
    refresh,
  };
}
