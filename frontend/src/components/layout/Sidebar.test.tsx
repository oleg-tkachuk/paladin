import { describe, expect, it } from "vitest";

import { ROLES } from "@/constants/roles";
import { cn } from "@/lib/utils";
import {
  BRAND_NAME_CLASS,
  GROUP_LABEL_CLASS,
  visibleNavigationGroups,
} from "./Sidebar";

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

  it("names the MCP entry after the page it opens", () => {
    const entry = visibleNavigationGroups([ROLES.platformAdmin])
      .flatMap((g) => g.items)
      .find((i) => i.path === "/mcp");
    expect(entry?.name).toBe("MCP server");
  });

  it("takes nothing away before the user is known", () => {
    expect(names(null)).toEqual(names([ROLES.platformAdmin]));
  });
});

// The label is rendered through cn(), which drops a class it reads as a
// clash: a size it does not know beside a text colour vanished, and the
// label came out larger than the items under it.
describe("group label", () => {
  it("keeps a font size through cn()", () => {
    expect(cn(GROUP_LABEL_CLASS).split(" ")).toContain("text-sm");
  });
});

// The release under the name is 14px monospace, which reads larger than
// 14px sans: the name sits a size above it so it does not look the smaller.
describe("brand name", () => {
  it("is set a size above the release line", () => {
    expect(cn(BRAND_NAME_CLASS).split(" ")).toContain("text-base");
  });
});
