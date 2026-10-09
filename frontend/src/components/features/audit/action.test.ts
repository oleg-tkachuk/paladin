import { describe, expect, it } from "vitest";

import { UNKNOWN_ACTION, parseAuditAction, parseAuditError } from "./action";

describe("parseAuditAction", () => {
  it.each([
    [
      "/paladin.admin.v1.TenantService/PurgeTenant",
      { name: "PurgeTenant", scope: "admin · TenantService", kind: "destroy" },
    ],
    [
      "/paladin.admin.v1.TenantService/DeleteTenant",
      { name: "DeleteTenant", scope: "admin · TenantService", kind: "destroy" },
    ],
    [
      "/paladin.iam.v1.UserService/CreateUser",
      { name: "CreateUser", scope: "iam · UserService", kind: "create" },
    ],
    [
      "/paladin.admin.v1.CapabilityService/Issue",
      { name: "Issue", scope: "admin · CapabilityService", kind: "create" },
    ],
    // The old substring palette read "Budget" as a get and painted it info.
    [
      "/paladin.admin.v1.TenantBudgetService/Set",
      { name: "Set", scope: "admin · TenantBudgetService", kind: "change" },
    ],
    [
      "/paladin.admin.v1.TenantBudgetService/Summarize",
      { name: "Summarize", scope: "admin · TenantBudgetService", kind: "read" },
    ],
    [
      "/paladin.iam.v1.AuthService/ExchangeAudience",
      { name: "ExchangeAudience", scope: "iam · AuthService", kind: "other" },
    ],
    [
      "iam.bootstrap_admin.reset",
      { name: "reset", scope: "iam.bootstrap_admin", kind: "change" },
    ],
    [
      "auth.refresh_token.reuse",
      { name: "reuse", scope: "auth.refresh_token", kind: "destroy" },
    ],
    ["login", { name: "login", scope: "", kind: "create" }],
    ["", { name: UNKNOWN_ACTION, scope: "", kind: "other" }],
  ])("%s", (action, want) => {
    expect(parseAuditAction(action)).toEqual(want);
  });
});

describe("parseAuditError", () => {
  it("splits the Connect code from the message", () => {
    expect(
      parseAuditError("failed_precondition: tenant still has rows: buckets"),
    ).toEqual({
      code: "failed_precondition",
      message: "tenant still has rows: buckets",
    });
  });

  it.each([
    ["no code at all", "boom"],
    ["a prefix that is not a Connect code", "storage: bucket gone"],
  ])("keeps %s whole", (_, msg) => {
    expect(parseAuditError(msg)).toEqual({ message: msg });
  });
});
