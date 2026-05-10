"use client";

// ObjectKeyContext — exposes the resolved ObjectKey fetched once
// by the OK detail layout. Mirrors BucketContext (Phase 2).
//
// `setObjectKey` lets a tab swap the cache after a mutation
// (UpdateObjectKey / BindObjectKeyToBucket / SetObjectKeyPolicy
// all return the updated resource — pushing it back keeps sibling
// tabs honest without a refetch).

import {
  createContext,
  useContext,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

import type { ObjectKey } from "@/gen/paladin/admin/v1/types_pb";

interface ObjectKeyContextValue {
  objectKey: ObjectKey;
  setObjectKey: Dispatch<SetStateAction<ObjectKey | null>>;
  refetch: () => Promise<void>;
}

const Ctx = createContext<ObjectKeyContextValue | null>(null);

export function ObjectKeyProvider({
  value,
  children,
}: {
  value: ObjectKeyContextValue;
  children: ReactNode;
}) {
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useObjectKey(): ObjectKeyContextValue {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useObjectKey must be used inside <ObjectKeyProvider>");
  }
  return ctx;
}
