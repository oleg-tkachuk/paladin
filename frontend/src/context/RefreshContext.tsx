"use client";

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";

/**
 * RefreshContext — global "something changed, please refetch" signal.
 *
 * Each topic has a counter. Mutation handlers call `bump(topic)` after a
 * successful create/update/delete; list/counter hooks subscribe via
 * `useRefreshSignal(topic)` and pass the returned number into the dep
 * array of the useEffect that triggers the fetch.
 *
 * Why a counter instead of a pub-sub event bus: the value is reactive
 * out of the box, so subscribers get re-renders without any extra wiring,
 * and depending on a number in useEffect is the most idiomatic React
 * way to express "refetch when this changes".
 *
 * Topics are deliberately coarse-grained. Pages that show counts spanning
 * several resource types subscribe to multiple topics. The only cost of a
 * spurious bump is one extra list call.
 */

export type RefreshTopic =
  | "objects" // Object create/delete/restore/update — affects /objects, /trash, /object-tags, dashboard counts.
  | "objectKeys" // ObjectKey create/delete — affects /object-keys, scope picker, sidebar counts.
  | "buckets" // Bucket create/delete/update — affects /buckets, scope picker.
  | "backends" // Backend create/delete/update — affects /backends, scope picker.
  | "tenants" // Tenant create/delete/update — affects /tenants, sidebar.
  | "users" // User create/delete/role change — affects /users.
  | "tokens" // API token create/revoke — affects /api-tokens.
  | "auditLogs"; // Audit log entries — affects /audit-logs and any audit-aware widget.

type Counters = Partial<Record<RefreshTopic, number>>;

interface RefreshContextValue {
  counters: Counters;
  bump: (topic: RefreshTopic | RefreshTopic[]) => void;
}

const RefreshContext = createContext<RefreshContextValue | undefined>(
  undefined,
);

export function RefreshProvider({ children }: { children: ReactNode }) {
  const [counters, setCounters] = useState<Counters>({});

  const bump = useCallback((topic: RefreshTopic | RefreshTopic[]) => {
    const topics = Array.isArray(topic) ? topic : [topic];
    if (topics.length === 0) return;
    setCounters((prev) => {
      const next = { ...prev };
      for (const t of topics) next[t] = (next[t] ?? 0) + 1;
      return next;
    });
  }, []);

  const value = useMemo<RefreshContextValue>(
    () => ({ counters, bump }),
    [counters, bump],
  );

  return (
    <RefreshContext.Provider value={value}>{children}</RefreshContext.Provider>
  );
}

function useRefreshContext(): RefreshContextValue {
  const ctx = useContext(RefreshContext);
  // No-op fallback so hooks that subscribe outside the provider tree
  // (e.g., on /login) don't crash. Bumps from there are silently dropped,
  // which is the correct behaviour — there's nothing to refresh.
  if (!ctx) return { counters: {}, bump: () => {} };
  return ctx;
}

/**
 * Subscribe to one or more topics. Returns a number that increments each
 * time any of the topics is bumped — pass it into a useEffect dep array
 * to trigger a refetch.
 */
export function useRefreshSignal(topic: RefreshTopic | RefreshTopic[]): number {
  const { counters } = useRefreshContext();
  const topics = Array.isArray(topic) ? topic : [topic];
  let sum = 0;
  for (const t of topics) sum += counters[t] ?? 0;
  return sum;
}

/**
 * Get the bump function. Mutation handlers call this after a successful
 * create/update/delete to fan out the refresh to subscribers.
 */
export function useBumpRefresh(): RefreshContextValue["bump"] {
  return useRefreshContext().bump;
}
