import { SECTION } from "@/lib/navigation/sections";
import { describe, expect, it } from "vitest";

import { ROLES } from "@/constants/roles";
import { cn } from "@/lib/utils";
import { ACCOUNT_PAGES } from "@/lib/navigation/account";
import {
  ACCOUNT_GROUP,
  BRAND_NAME_CLASS,
  GROUP_LABEL_CLASS,
  SESSION_TENANT_GROUP,
  activeNavHref,
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
      // A user's own OAuth grants, and what acts for them, read from IAM.
      "Connected applications",
      "My capabilities",
    ]);
  });

  // The account group holds the user's own pages and nothing of a tenant's
  // or the platform's; the platform's configuration is a page of System.
  it("groups the user's own pages under Account, the configuration under System", () => {
    const groups = visibleNavigationGroups([ROLES.platformAdmin]);
    const account = groups.find((g) => g.title === ACCOUNT_GROUP);
    expect(account?.items.map((i) => i.path)).toEqual(
      ACCOUNT_PAGES.map((p) => p.path),
    );
    const system = groups.find((g) => g.title === "System");
    expect(system?.items.map((i) => i.path)).toContain(SECTION.config);
    expect(groups.map((g) => g.title)).not.toContain("Settings");
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

  // The operations page reads the admin plane, as the drawer does.
  it("lists Operations under System, for the admin plane only", () => {
    const system = visibleNavigationGroups([ROLES.platformAdmin]).find(
      (g) => g.title === "System",
    );
    expect(system?.items.map((i) => i.path)).toContain(SECTION.operations);
    expect(names([ROLES.tenantUser])).not.toContain("Operations");
  });

  // The MCP server is a group of its own: the four questions it answers are
  // pages, each a row, and none of them a row of System any more.
  it("gives the MCP server a group of its pages", () => {
    const groups = visibleNavigationGroups([ROLES.platformAdmin]);
    const mcp = groups.find((g) => g.title === "MCP");
    expect(mcp?.items.map((i) => i.path)).toEqual([
      SECTION.mcp,
      SECTION.mcpTools,
      SECTION.mcpAccess,
      SECTION.mcpConnect,
    ]);
    const system = groups.find((g) => g.title === "System");
    expect(system?.items.some((i) => i.path?.startsWith(SECTION.mcp))).toBe(
      false,
    );
  });

  // Every MCP page reads the admin plane, so a principal without it gets
  // none of them rather than four rows that fail.
  it("leaves the MCP group to the admin plane", () => {
    const groups = visibleNavigationGroups([ROLES.tenantUser]);
    expect(groups.find((g) => g.title === "MCP")).toBeUndefined();
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
