"use client";

// BucketContext — exposes the resolved Bucket fetched once by the
// bucket-detail layout. Child tabs (Overview / Lifecycle / Policy /
// …) read it instead of re-fetching, so tab switches are instant.
//
// `setBucket` lets a tab update the cache after a mutation (e.g.
// SetLifecycleRules returns the updated Bucket — Lifecycle pushes it
// back so Overview's rule count refreshes without a round-trip).

import {
  createContext,
  useContext,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";

interface BucketContextValue {
  bucket: Bucket;
  setBucket: Dispatch<SetStateAction<Bucket | null>>;
  /** Force a re-fetch from the layout. Used when an action knows the
   *  server-side state moved out from under the cached copy
   *  (Aborted concurrency error, manual Refresh button). */
  refetch: () => Promise<void>;
}

const BucketCtx = createContext<BucketContextValue | null>(null);

export function BucketProvider({
  value,
  children,
}: {
  value: BucketContextValue;
  children: ReactNode;
}) {
  return <BucketCtx.Provider value={value}>{children}</BucketCtx.Provider>;
}

export function useBucket(): BucketContextValue {
  const ctx = useContext(BucketCtx);
  if (!ctx) {
    throw new Error("useBucket must be used inside <BucketProvider>");
  }
  return ctx;
}
