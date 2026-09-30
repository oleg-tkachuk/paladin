import { describe, expect, it } from "vitest";

import { ROLES } from "@/constants/roles";
import { visibleNavigationGroups } from "./Sidebar";

const names = (roles: string[] | null) =>
  visibleNavigationGroups(roles).flatMap((g) => g.items.map((i) => i.name));

describe("visibleNavigationGroups", () => {
  // Every other view reads the admin plane, which IAM never opens to a pure
  // tenant.user; listing them only led to pages of refusals.
  it("gives a tenant.user only what works without the admin plane", () => {
    expect(names([ROLES.tenantUser])).toEqual([
      "Dashboard",
      "Health Status",
      "Profile",
    ]);
  });

  it("drops the groups left empty", () => {
    const titles = visibleNavigationGroups([ROLES.tenantUser]).map(
      (g) => g.title,
    );
    expect(titles).not.toContain("Management");
    expect(titles).not.toContain("Agents");
  });

  it("gives an admin-tier role everything", () => {
    const all = names([ROLES.platformAdmin]);
    expect(all).toContain("Tenants");
    expect(all).toContain("Audit Logs");
    expect(all).toEqual(names([ROLES.tenantAdmin]));
  });

  it("takes nothing away before the user is known", () => {
    expect(names(null)).toEqual(names([ROLES.platformAdmin]));
  });
});
