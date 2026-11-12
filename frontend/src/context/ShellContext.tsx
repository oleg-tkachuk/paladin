"use client";

// ShellContext — the console chrome's own state.
//
// The iam plane's version and health, the caller's operations, and the
// operator's banner. Each section is fetched and reported on its own, so a
// health probe that is down greys one badge instead of taking the page with
// it. The RPCs go through the session role like every other call (ADR-0048);
// the banner is the console's own configuration, served by /api/banner.

import React, {
  useEffect,
  useRef,
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import {
  type VersionInfo,
  type HealthInfo,
} from "@/gen/paladin/iam/v1/health_service_pb";
import { type Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { adminOperationClient, iamHealthClient } from "@/lib/connect/client";
import { BANNER_PATH } from "@/constants";
import { settledSection } from "@/lib/shell/section";
import { canUseAdminPlane } from "@/constants/roles";
import { useAuth } from "@/context/AuthContext";
import { useVisiblePolling } from "@/hooks/useVisiblePolling";
import { usePreference } from "@/hooks/usePreference";
import { PREFERENCE_KEYS, readFlag } from "@/lib/preferences/keys";
import { anyActive } from "@/lib/operationNotices";
import { bannerFromJson, type PlatformBanner } from "@/lib/banner";

export type SectionStatus = "ok" | "unavailable" | "loading";

export interface ShellSection<T> {
  status: SectionStatus;
  reason?: string;
  data: T | null;
}

interface ShellState {
  version: ShellSection<VersionInfo>;
  health: ShellSection<HealthInfo>;
  operations: ShellSection<Operation[]>;
  /** The operator's banner (/api/banner), or null. */
  banner: PlatformBanner | null;
  /** Set when the aggregate itself could not be reached at all. */
  error: string | null;
  lastUpdated: Date | null;
  refresh: () => Promise<void>;
}

const LOADING: ShellSection<never> = { status: "loading", data: null };
const POLL_INTERVAL = 10_000;
const NO_OPERATIONS: Operation[] = [];

const ShellContext = createContext<ShellState | undefined>(undefined);

/** How many of the caller's operations the drawer lists. */
const OPERATIONS_PAGE = 50;

/** A section unavailable because the shell itself failed. */
function unavailable(reason: string): ShellSection<never> {
  return { status: "unavailable", reason, data: null };
}

export function ShellProvider({ children }: { children: ReactNode }) {
  const [version, setVersion] = useState<ShellSection<VersionInfo>>(LOADING);
  const [health, setHealth] = useState<ShellSection<HealthInfo>>(LOADING);
  const [operations, setOperations] =
    useState<ShellSection<Operation[]>>(LOADING);
  const [banner, setBanner] = useState<PlatformBanner | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  // IAM never issues a pure tenant.user the admin audience; asking on every
  // poll only collects a refusal. The aggregate serves the iam sections alone.
  const { user } = useAuth();
  const adminAllowed = canUseAdminPlane(user?.roles ?? []);
  // Who the shell is for; null until someone is signed in. The provider is
  // mounted on the login page too, where asking only collects a 401 — and the
  // chrome then read "Unknown" after sign-in until the next poll.
  const session = user ? `${user.tenantId ?? ""}/${user.userId ?? ""}` : null;
  const inFlight = useRef<Promise<void> | null>(null);

  const fetchShell = useCallback(async () => {
    try {
      const [v, h, ops, bannerRes] = await Promise.allSettled([
        iamHealthClient.getVersion({}),
        iamHealthClient.getHealth({}),
        // IAM never issues a pure tenant.user the admin audience; asking
        // only collects a refusal.
        adminAllowed
          ? adminOperationClient.listOperations({
              page: { pageSize: OPERATIONS_PAGE, pageToken: "" },
              filter: "",
              sortOrder: SortOrder.DESC,
              mine: true,
            })
          : Promise.reject(new Error("the admin plane is not this user's")),
        fetch(BANNER_PATH, { cache: "no-store" }).then((r) => {
          if (!r.ok) throw new Error(`banner: HTTP ${r.status}`);
          return r.json() as Promise<unknown>;
        }),
      ]);
      setVersion(settledSection(v));
      setHealth(settledSection(h));
      setOperations(settledSection(ops, (res) => res.operations));
      // An unreadable banner leaves the last banner up rather than taking it
      // down: the notice it carried is still the operator's.
      if (bannerRes.status === "fulfilled") {
        setBanner(bannerFromJson(bannerRes.value));
      }
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      setVersion(unavailable(message));
      setHealth(unavailable(message));
      setOperations(unavailable(message));
    }
  }, [adminAllowed]);

  // One fetch at a time: the poll and the sign-in refresh below can land in
  // the same tick.
  const refresh = useCallback(() => {
    if (!session) return Promise.resolve();
    inFlight.current ??= fetchShell().finally(() => {
      inFlight.current = null;
    });
    return inFlight.current;
  }, [fetchShell, session]);

  // A user who asked to hear when their operations finish is told even from
  // a hidden tab, so the list is watched there too — while something runs.
  const [notifyOperations] = usePreference(
    PREFERENCE_KEYS.notifyOperations,
    readFlag,
  );
  const watchHidden =
    notifyOperations && anyActive(operations.data ?? NO_OPERATIONS);
  useVisiblePolling(refresh, POLL_INTERVAL, true, watchHidden);

  // Ask as soon as someone is signed in, not on the next tick.
  useEffect(() => {
    void refresh();
  }, [refresh]);

  const value = useMemo<ShellState>(
    () => ({
      version,
      health,
      operations,
      banner,
      error,
      lastUpdated,
      refresh,
    }),
    [version, health, operations, banner, error, lastUpdated, refresh],
  );

  return (
    <ShellContext.Provider value={value}>{children}</ShellContext.Provider>
  );
}

export function useShell() {
  const ctx = useContext(ShellContext);
  if (!ctx) {
    throw new Error("useShell must be used within a ShellProvider");
  }
  return ctx;
}
