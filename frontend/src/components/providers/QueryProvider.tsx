"use client";

import { useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { QUERY_STALE_MS } from "@/constants";

/**
 * App-wide TanStack Query provider. Adopted to replace the
 * setState-in-effect fetch-on-mount pattern across the data hooks /
 * pages (react-hooks v6 `set-state-in-effect`).
 *
 * Defaults are deliberately CONSERVATIVE so the migration doesn't change
 * observable behaviour from the hand-rolled hooks it replaces:
 *   - refetchOnWindowFocus: false — the old hooks fetched on mount + on
 *     explicit refresh()/refreshSignal only, never on focus.
 *   - retry: 1 — one retry on transient failure; the old hooks didn't
 *     retry, but a single retry is a strict reliability improvement with
 *     no behavioural surprise (errors still surface).
 *   - staleTime: 30s — within a view, avoid re-fetching the same key on
 *     remount; cross-mutation freshness is driven by explicit
 *     invalidateQueries (the refreshSignal replacement), not staleness.
 */
export function QueryProvider({ children }: { children: ReactNode }) {
  // One client per browser session, created lazily so it isn't shared
  // across SSR requests (Next.js app-router guidance).
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            refetchOnWindowFocus: false,
            retry: 1,
            staleTime: QUERY_STALE_MS,
          },
        },
      }),
  );
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
