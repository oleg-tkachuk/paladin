import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

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
