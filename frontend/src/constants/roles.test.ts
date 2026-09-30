import { describe, expect, it } from "vitest";

import { ROLES, canUseAdminPlane } from "./roles";

describe("canUseAdminPlane", () => {
  it.each([
    [[ROLES.platformAdmin]],
    [[ROLES.tenantAdmin]],
    [[ROLES.bucketAdmin]],
    [[ROLES.iamAdmin]],
    [[ROLES.tenantUser, ROLES.tenantAdmin]],
    [[ROLES.tenantProvisioner]],
    [[ROLES.capabilityIssuer]],
  ])("admits %j", (roles) => {
    expect(canUseAdminPlane(roles)).toBe(true);
  });

  // The same principals IAM refuses the paladin-admin audience.
  it.each([
    [[]],
    [[ROLES.tenantUser]],
    [[ROLES.mcpOperator]],
    [["invented.admin"]],
  ])("refuses %j", (roles) => {
    expect(canUseAdminPlane(roles)).toBe(false);
  });
});
