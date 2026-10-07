/**
 * Scopes as the console shows and grants them. The labels are the server's
 * wire names (auth/scope.go: "type:value" in a token's `scopes` claim), so
 * what an operator reads here is what a token carries.
 */

import type { Scope } from "@/gen/paladin/common/v1/scope_pb";
import { ScopeType } from "@/gen/paladin/common/v1/scope_pb";

/** Matches every resource of a scope's type. */
export const SCOPE_WILDCARD = "*";

/** The scope types an operator can grant, in the order they nest. */
export const GRANTABLE_SCOPE_TYPES: readonly ScopeType[] = [
  ScopeType.TENANT,
  ScopeType.BACKEND,
  ScopeType.BUCKET,
  ScopeType.OBJECT_KEY,
];

const SCOPE_TYPE_WIRE: Readonly<Record<ScopeType, string>> = {
  [ScopeType.UNSPECIFIED]: "unspecified",
  [ScopeType.TENANT]: "tenant",
  [ScopeType.BACKEND]: "backend",
  [ScopeType.BUCKET]: "bucket",
  [ScopeType.OBJECT_KEY]: "collection",
};

/** What a value of each type looks like, for the grant form. */
export const SCOPE_VALUE_EXAMPLES: Readonly<Record<ScopeType, string>> = {
  [ScopeType.UNSPECIFIED]: "",
  [ScopeType.TENANT]: "tenant id",
  [ScopeType.BACKEND]: "backend id",
  [ScopeType.BUCKET]: "bucket name",
  [ScopeType.OBJECT_KEY]: "bucket/collection or bucket/collection/*",
};

export function scopeTypeName(type: ScopeType): string {
  return SCOPE_TYPE_WIRE[type];
}

/** The scope as a token carries it: "type:value". */
export function scopeWire(scope: Pick<Scope, "type" | "value">): string {
  return `${scopeTypeName(scope.type)}:${scope.value}`;
}
