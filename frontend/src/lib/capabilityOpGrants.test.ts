import { describe, expect, it } from "vitest";

import { CapabilityGrantBasis } from "@/gen/paladin/admin/v1/capability_service_pb";

import {
  capabilityOpGrants,
  procedureLabel,
  type CapabilityOpGrants,
  type OperationGrant,
} from "./capabilityOpGrants";

const READ_ACTIONS = ["GetObject", "PresignGet"];
const PUT_ACTIONS = ["PutObject", "PresignPut", "UpdateObject", "CopyObject"];
const PRESIGN_PAIRED = ["get", "put"];
const DELEGATE = "paladin.admin.v1.CapabilityService.Delegate";

function row(op: string, fields: Partial<OperationGrant>): OperationGrant {
  return {
    op,
    basis: CapabilityGrantBasis.BUILTIN,
    actions: [],
    pairedOps: [],
    procedures: [],
    ...fields,
  };
}

// The table as the server serves it (backend capability_grants.go), one row
// per basis.
const GRANTS: OperationGrant[] = [
  row("get", { actions: READ_ACTIONS }),
  row("list", { actions: READ_ACTIONS }),
  row("put", { actions: PUT_ACTIONS }),
  row("delete", { actions: ["DeleteObject"] }),
  row("manage", { basis: CapabilityGrantBasis.TENANT_POLICY }),
  row("object:presign", {
    basis: CapabilityGrantBasis.PAIRED,
    pairedOps: PRESIGN_PAIRED,
  }),
  row("share", {
    basis: CapabilityGrantBasis.CAVEATS_ONLY,
    procedures: [DELEGATE],
  }),
];

/** A basis number no generated enum value holds: a newer server's. */
const FUTURE_BASIS = 99 as CapabilityGrantBasis;

const NONE: CapabilityOpGrants = {
  granted: [],
  needsPolicy: [],
  paired: [],
  caveatsOnly: [],
  unknown: [],
};

describe("capabilityOpGrants", () => {
  it.each<[string, string[], readonly OperationGrant[], CapabilityOpGrants]>([
    ["no ops", [], GRANTS, NONE],
    [
      "BUILTIN: the actions it grants",
      ["get"],
      GRANTS,
      { ...NONE, granted: [{ op: "get", actions: READ_ACTIONS }] },
    ],
    [
      "TENANT_POLICY: needs a tenant policy",
      ["manage"],
      GRANTS,
      { ...NONE, needsPolicy: ["manage"] },
    ],
    [
      "PAIRED: the ops it is asserted beside",
      ["object:presign"],
      GRANTS,
      {
        ...NONE,
        paired: [{ op: "object:presign", pairedOps: PRESIGN_PAIRED }],
      },
    ],
    [
      "CAVEATS_ONLY: the procedures it opens",
      ["share"],
      GRANTS,
      { ...NONE, caveatsOnly: [{ op: "share", procedures: [DELEGATE] }] },
    ],
    [
      "an op the table does not name",
      ["object:tag"],
      GRANTS,
      { ...NONE, unknown: ["object:tag"] },
    ],
    [
      "UNSPECIFIED: unknown, even with actions",
      ["get"],
      [
        row("get", {
          basis: CapabilityGrantBasis.UNSPECIFIED,
          actions: READ_ACTIONS,
        }),
      ],
      { ...NONE, unknown: ["get"] },
    ],
    [
      "a basis this console does not know: unknown",
      ["get"],
      [row("get", { basis: FUTURE_BASIS, actions: READ_ACTIONS })],
      { ...NONE, unknown: ["get"] },
    ],
    [
      "BUILTIN with no actions: unknown, not granted",
      ["get"],
      [row("get", {})],
      { ...NONE, unknown: ["get"] },
    ],
    [
      "PAIRED with no ops: unknown",
      ["object:presign"],
      [row("object:presign", { basis: CapabilityGrantBasis.PAIRED })],
      { ...NONE, unknown: ["object:presign"] },
    ],
    [
      "CAVEATS_ONLY with no procedures: unknown",
      ["share"],
      [row("share", { basis: CapabilityGrantBasis.CAVEATS_ONLY })],
      { ...NONE, unknown: ["share"] },
    ],
    [
      // A TENANT_POLICY row grants nothing whatever it carries.
      "TENANT_POLICY with actions: still needs a policy",
      ["manage"],
      [
        row("manage", {
          basis: CapabilityGrantBasis.TENANT_POLICY,
          actions: ["DeleteObject"],
        }),
      ],
      { ...NONE, needsPolicy: ["manage"] },
    ],
    [
      "every basis, in the order given",
      ["share", "put", "object:tag", "manage", "object:presign", "get"],
      GRANTS,
      {
        granted: [
          { op: "put", actions: PUT_ACTIONS },
          { op: "get", actions: READ_ACTIONS },
        ],
        needsPolicy: ["manage"],
        paired: [{ op: "object:presign", pairedOps: PRESIGN_PAIRED }],
        caveatsOnly: [{ op: "share", procedures: [DELEGATE] }],
        unknown: ["object:tag"],
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

describe("procedureLabel", () => {
  it.each<[string, string, string]>([
    ["drops the package", DELEGATE, "CapabilityService.Delegate"],
    [
      "keeps Service.Method",
      "CapabilityService.Delegate",
      "CapabilityService.Delegate",
    ],
    ["keeps a bare name", "Delegate", "Delegate"],
    ["keeps an empty name", "", ""],
  ])("%s", (_name, procedure, want) => {
    expect(procedureLabel(procedure)).toBe(want);
  });
});
