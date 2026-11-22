import { describe, expect, it } from "vitest";

import {
  ROLES,
  canManageTenantBudgets,
  canUseAdminPlane,
  isPlatformAdmin,
} from "./roles";

describe("canManageTenantBudgets", () => {
  it("is the platform admin's alone", () => {
    expect(canManageTenantBudgets([ROLES.platformAdmin])).toBe(true);
  });

  it.each([
    [[ROLES.tenantAdmin]],
    [[ROLES.bucketAdmin]],
    [[ROLES.iamAdmin]],
    [[ROLES.tenantProvisioner]],
    [[ROLES.capabilityIssuer]],
    [[]],
  ])("refuses %j", (roles) => {
    expect(canManageTenantBudgets(roles)).toBe(false);
  });
});

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

describe("isPlatformAdmin", () => {
  it("is the platform.admin role alone", () => {
    expect(isPlatformAdmin([ROLES.tenantAdmin, ROLES.platformAdmin])).toBe(
      true,
    );
    expect(isPlatformAdmin([ROLES.tenantAdmin, ROLES.tenantProvisioner])).toBe(
      false,
    );
    expect(isPlatformAdmin([])).toBe(false);
  });
});
