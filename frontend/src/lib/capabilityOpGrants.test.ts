import { describe, expect, it } from "vitest";

import {
  capabilityOpGrants,
  needsTenantPolicy,
  type CapabilityOpGrants,
  type OperationGrant,
} from "./capabilityOpGrants";

// The table as the server serves it today (backend capability_grants.go).
const READ_ACTIONS = ["GetObject", "PresignGet"];
const PUT_ACTIONS = ["PutObject", "PresignPut", "UpdateObject", "CopyObject"];
const GRANTS: OperationGrant[] = [
  { op: "get", actions: READ_ACTIONS },
  { op: "list", actions: READ_ACTIONS },
  { op: "put", actions: PUT_ACTIONS },
  { op: "delete", actions: ["DeleteObject"] },
  { op: "manage", actions: [] },
];

const NONE: CapabilityOpGrants = { granted: [], needsPolicy: [], unknown: [] };

describe("capabilityOpGrants", () => {
  it.each<[string, string[], readonly OperationGrant[], CapabilityOpGrants]>([
    ["no ops", [], GRANTS, NONE],
    [
      "an op that unlocks actions",
      ["get"],
      GRANTS,
      { ...NONE, granted: [{ op: "get", actions: READ_ACTIONS }] },
    ],
    [
      "an op the table names with no actions",
      ["manage"],
      GRANTS,
      { ...NONE, needsPolicy: ["manage"] },
    ],
    [
      "an op the table does not name",
      ["share"],
      GRANTS,
      { ...NONE, unknown: ["share"] },
    ],
    [
      "every outcome, in the order given",
      ["share", "put", "manage", "get", "object:tag"],
      GRANTS,
      {
        granted: [
          { op: "put", actions: PUT_ACTIONS },
          { op: "get", actions: READ_ACTIONS },
        ],
        needsPolicy: ["manage"],
        unknown: ["share", "object:tag"],
      },
    ],
    [
      "an op given twice, once",
      ["get", "manage", "get", "manage"],
      GRANTS,
      {
        ...NONE,
        granted: [{ op: "get", actions: READ_ACTIONS }],
        needsPolicy: ["manage"],
      },
    ],
    [
      "an empty table: every op unknown",
      ["get", "manage"],
      [],
      { ...NONE, unknown: ["get", "manage"] },
    ],
  ])("%s", (_name, ops, grants, want) => {
    expect(capabilityOpGrants(ops, grants)).toEqual(want);
  });

  it("takes a set, as the Issue dialog holds its ops", () => {
    expect(capabilityOpGrants(new Set(["delete"]), GRANTS)).toEqual({
      ...NONE,
      granted: [{ op: "delete", actions: ["DeleteObject"] }],
    });
  });
});

describe("needsTenantPolicy", () => {
  it.each<[string, OperationGrant, boolean]>([
    ["no actions", { op: "manage", actions: [] }, true],
    ["one action", { op: "delete", actions: ["DeleteObject"] }, false],
  ])("%s", (_name, grant, want) => {
    expect(needsTenantPolicy(grant)).toBe(want);
  });
});
