import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";

import { OP_CHOICES, principalKindLabel } from "./_constants";

// The table of capability operations both SDKs and the server are held to.
const SHARED_OPS = resolve(
  __dirname,
  "../../../../../../sdk/testdata/capability_ops.json",
);

describe("capability op choices", () => {
  it("offer exactly limes's operations, then the data plane's own", () => {
    const table = JSON.parse(readFileSync(SHARED_OPS, "utf8")) as {
      limes: string[];
      data_plane: Record<string, string>;
    };
    expect(OP_CHOICES).toEqual([
      ...table.limes,
      ...Object.values(table.data_plane),
    ]);
  });
});

describe("principalKindLabel", () => {
  it("names each kind, and the number of one it does not know", () => {
    expect(principalKindLabel(PrincipalKind.USER)).toBe("user");
    expect(principalKindLabel(PrincipalKind.AGENT)).toBe("agent");
    expect(principalKindLabel(PrincipalKind.SERVICE)).toBe("service");
    expect(principalKindLabel(PrincipalKind.UNSPECIFIED)).toBe("kind:0");
  });
});
