import { describe, expect, it } from "vitest";

import { ScopeType } from "@/gen/paladin/common/v1/scope_pb";
import { GRANTABLE_SCOPE_TYPES, scopeWire } from "./scopes";

// The console shows a scope as a token carries it (auth/scope.go): the
// OBJECT_KEY type travels as "collection".
describe("scopeWire", () => {
  it.each([
    [ScopeType.TENANT, "t-1", "tenant:t-1"],
    [ScopeType.BACKEND, "primary", "backend:primary"],
    [ScopeType.BUCKET, "*", "bucket:*"],
    [ScopeType.OBJECT_KEY, "media/photos/*", "collection:media/photos/*"],
  ])("%s %s → %s", (type, value, wire) => {
    expect(scopeWire({ type, value })).toBe(wire);
  });

  it("offers no unspecified type to grant", () => {
    expect(GRANTABLE_SCOPE_TYPES).not.toContain(ScopeType.UNSPECIFIED);
  });
});
