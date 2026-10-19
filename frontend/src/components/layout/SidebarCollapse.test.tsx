import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// The pinned and recent resources are tested with their hook.
vi.mock("@/hooks/useResourceLinks", () => ({
  useResourceLinks: () => ({
    pinned: [],
    recent: [],
    visit: () => {},
    togglePin: async () => {},
    isPinned: () => false,
  }),
}));
vi.mock("next/navigation", async (importOriginal) => ({
  ...(await importOriginal<typeof import("next/navigation")>()),
  usePathname: () => "/",
  useRouter: () => ({ push: vi.fn() }),
}));
const TENANT_ID = "01a0f4a9-7600-77a9-862e-11ea31d523ca";
const TENANT_NAME = "Platform";
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    user: { subject: "admin", roles: ["platform.admin"], tenantId: TENANT_ID },
    logout: vi.fn(),
  }),
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenant: { displayName: TENANT_NAME } }),
}));
const acting = vi.hoisted(() => ({
  tenant: null as {
    tenantId: string;
    slug: string;
    displayName: string;
  } | null,
}));
vi.mock("@/hooks/useActingTenant", () => ({
  useActingTenant: () => acting.tenant,
}));
vi.mock("@/components/layout/BuildVersion", () => ({
  BuildVersion: () => null,
}));

import { TooltipProvider } from "@/components/ui/Tooltip";
import { SESSION_TENANT_GROUP, Sidebar } from "./Sidebar";
import { SIDEBAR_COLLAPSED_ATTR } from "./sidebarWidth";

// A bar fixed to the viewport clears the sidebar by reading this attribute
// (sidebarWidth.CLEAR_SIDEBAR); without it the bar would sit under a
// collapsed sidebar's freed space instead of centring on the content.
describe("Sidebar collapse", () => {
  it("marks itself collapsed, and unmarks itself when expanded", async () => {
    const { container } = render(
      <TooltipProvider>
        <Sidebar isOpen={false} onClose={vi.fn()} />
      </TooltipProvider>,
    );
    const aside = container.querySelector("aside")!;
    expect(aside).not.toHaveAttribute(SIDEBAR_COLLAPSED_ATTR);

    await userEvent.click(
      screen.getByRole("button", { name: "Collapse sidebar" }),
    );
    expect(aside).toHaveAttribute(SIDEBAR_COLLAPSED_ATTR);

    await userEvent.click(
      screen.getByRole("button", { name: "Expand sidebar" }),
    );
    expect(aside).not.toHaveAttribute(SIDEBAR_COLLAPSED_ATTR);
  });

  // The group's heading is the short label every group has; the tenant is
  // named on its own line, cut to the sidebar's width, its id on hover.
  it("names the session tenant under its group's heading", () => {
    render(
      <TooltipProvider>
        <Sidebar isOpen={false} onClose={vi.fn()} />
      </TooltipProvider>,
    );
    expect(screen.getByText(SESSION_TENANT_GROUP)).toBeInTheDocument();
    const name = screen.getByTestId("session-tenant-name");
    expect(name).toHaveTextContent(TENANT_NAME);
    expect(name).toHaveAttribute("title", TENANT_ID);
    expect(name).toHaveClass("truncate");
  });
});

// The g-jump that lands on a row is named beside it, from the same table the
// handler serves (lib/navShortcuts), so the hint cannot name another place.
describe("Sidebar shortcut hints", () => {
  const caps = (name: string) =>
    [...screen.getByRole("link", { name }).querySelectorAll("kbd")].map(
      (k) => k.textContent,
    );

  it("names a row's jump beside it, and none where no jump lands", () => {
    acting.tenant = null;
    render(
      <TooltipProvider>
        <Sidebar isOpen={false} onClose={vi.fn()} />
      </TooltipProvider>,
    );
    expect(caps("Audit Logs")).toEqual(["G", "A"]);
    expect(caps("Users")).toEqual(["G", "U"]);
    expect(caps("Upload")).toEqual([]);
  });

  // With a tenant on screen, g b goes to its buckets, not to the list of all.
  it("names no jump on a row the scoped jump passes by", () => {
    acting.tenant = { tenantId: TENANT_ID, slug: "acme", displayName: "Acme" };
    render(
      <TooltipProvider>
        <Sidebar isOpen={false} onClose={vi.fn()} />
      </TooltipProvider>,
    );
    expect(caps("Buckets")).toEqual([]);
    expect(caps("Audit Logs")).toEqual(["G", "A"]);
    acting.tenant = null;
  });
});
