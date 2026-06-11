"use client";

import { useEffect, useState } from "react";

import { useScope } from "@/context/ScopeContext";

export interface SidebarCounts {
  objects: number | null;
  tenants: number | null;
  buckets: number | null;
  objectKeys: number | null;
  objectTags: number | null;
  trash: number | null;
}

const EMPTY: SidebarCounts = {
  objects: null,
  tenants: null,
  buckets: null,
  objectKeys: null,
  objectTags: null,
  trash: null,
};

/**
 * Fetches the badge counts the sidebar shows next to each nav item.
 *
 * Object/Trash counts are scoped to the current `objectKey` (from
 * ScopeContext) — they reflect the namespace the user is actively
 * browsing. The other counts (tenants/buckets/keys/tags) are tenant-
 * wide via List* RPCs.
 */
async function fetchCounts(_objectKey: string): Promise<SidebarCounts> {
  // Parked during proto migration. List/Count request shapes diverged
  // (no pageSize/objectKey/backendId at top level). Sidebar badges stay null
  // until the new pagination/filter model is wired up.
  return EMPTY;
}

/**
 * Lightweight hook for sidebar badges. Polls every 30s; refetches
 * immediately when the user switches ObjectKey scope so the Objects /
 * Trash badges reflect the new namespace without waiting for the next
 * poll tick.
 */
export function useSidebarCounts() {
  const { objectKey } = useScope();
  const [counts, setCounts] = useState<SidebarCounts>(EMPTY);

  useEffect(() => {
    let cancelled = false;

    const poll = () => {
      fetchCounts(objectKey).then((result) => {
        if (!cancelled) setCounts(result);
      });
    };

    poll();
    // Tick only while the tab is visible; one catch-up poll on return
    // so badges are fresh without a 30s background loop.
    const tick = () => {
      if (!document.hidden) poll();
    };
    const interval = setInterval(tick, 30_000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      cancelled = true;
      clearInterval(interval);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [objectKey]);

  return counts;
}
