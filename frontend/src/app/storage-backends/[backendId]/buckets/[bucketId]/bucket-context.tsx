"use client";

// BucketContext — the bucket the bucket layout fetched once, for its tabs
// (Overview / Collections / Lifecycle / Policy / …), so a tab switch is
// instant. `setBucket` lets a tab cache the Bucket a mutation returns, and
// `refetch` re-reads it when the server moved under the cached copy.

import {
  createContext,
  useContext,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";

/** The tenant that owns the bucket, as its page names it. */
export interface BucketOwner {
  tenantId: string;
  slug: string;
  displayName: string;
  /** In the trash: the owner, and so its bucket, takes no changes. */
  trashed: boolean;
}

interface BucketContextValue {
  bucket: Bucket;
  setBucket: Dispatch<SetStateAction<Bucket | null>>;
  refetch: () => Promise<void>;
  /** The owner once read; null while it loads or when it cannot be read. */
  owner: BucketOwner | null;
  /** Why a change is refused before it is sent, or null when it is not. */
  changesBlocked: string | null;
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
