import { describe, expect, it } from "vitest";

import { ROLES } from "@/constants/roles";
import { cn } from "@/lib/utils";
import {
  BRAND_NAME_CLASS,
  GROUP_LABEL_CLASS,
  SESSION_TENANT_GROUP,
  activeNavHref,
  sessionTenantGroupTitle,
  visibleNavigationGroups,
  type NavTarget,
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
    expect(titles).not.toContain(SESSION_TENANT_GROUP);
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

// The release under the name is a monospace, which reads larger than its
// size: the name sits above it so it does not look the smaller.
describe("brand name", () => {
  it("is set a size above the release line", () => {
    expect(cn(BRAND_NAME_CLASS).split(" ")).toContain("text-base");
  });
});

describe("activeNavHref", () => {
  const targets: NavTarget[] = [
    { href: "/storage-backends", tenantTab: false },
    { href: "/buckets", tenantTab: false },
    { href: "/tenants", tenantTab: false },
    { href: "/tenants/acme/capabilities", tenantTab: true },
  ];

  it.each([
    ["/storage-backends/primary", "/storage-backends"],
    // A bucket highlights Buckets, not the backend its URL sits under.
    ["/storage-backends/primary/buckets/b1/policy", "/buckets"],
    ["/tenants/other/collections", "/tenants"],
    // The session tenant's shortcut, alone — not Tenants with it.
    ["/tenants/acme/capabilities", "/tenants/acme/capabilities"],
    ["/tenants/acme/capabilities/c1", "/tenants/acme/capabilities"],
    ["/somewhere-else", null],
  ])("%s lights %s", (path, want) => {
    expect(activeNavHref(path, targets)).toBe(want);
  });
});

describe("sessionTenantGroupTitle", () => {
  it("names the session tenant once it is known", () => {
    expect(sessionTenantGroupTitle("acme")).toBe(
      `${SESSION_TENANT_GROUP} · acme`,
    );
    expect(sessionTenantGroupTitle(null)).toBe(SESSION_TENANT_GROUP);
  });
});

describe("the session tenant's group", () => {
  // Its rows open the session tenant's workspace; a platform page there reads
  // as that tenant's. Billing spans every tenant, so it is a System row.
  it("holds only shortcuts into the workspace", () => {
    const group = visibleNavigationGroups(null).find(
      (g) => g.title === SESSION_TENANT_GROUP,
    );
    expect(group?.items.every((i) => i.tenantTab)).toBe(true);
    const system = visibleNavigationGroups(null).find(
      (g) => g.title === "System",
    );
    expect(system?.items.map((i) => i.name)).toContain("Billing");
  });
});
