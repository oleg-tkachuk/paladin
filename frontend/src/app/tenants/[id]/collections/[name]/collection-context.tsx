"use client";

// CollectionContext — exposes the resolved Collection fetched once
// by the Collection detail layout. Mirrors BucketContext (Phase 2).
//
// `setCollection` lets a tab swap the cache after a mutation
// (UpdateCollection / BindCollectionToBucket / SetCollectionPolicy
// all return the updated resource — pushing it back keeps sibling
// tabs honest without a refetch).

import {
  createContext,
  useContext,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

import type { Collection } from "@/gen/paladin/admin/v1/types_pb";

interface CollectionContextValue {
  collection: Collection;
  setCollection: Dispatch<SetStateAction<Collection | null>>;
  refetch: () => Promise<void>;
}

const Ctx = createContext<CollectionContextValue | null>(null);

export function CollectionProvider({
  value,
  children,
}: {
  value: CollectionContextValue;
  children: ReactNode;
}) {
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useCollection(): CollectionContextValue {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useCollection must be used inside <CollectionProvider>");
  }
  return ctx;
}
