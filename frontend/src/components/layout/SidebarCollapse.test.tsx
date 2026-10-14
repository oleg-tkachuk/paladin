import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    user: { subject: "admin", roles: ["platform.admin"] },
    logout: vi.fn(),
  }),
}));
vi.mock("@/components/layout/BuildVersion", () => ({
  BuildVersion: () => null,
}));

import { TooltipProvider } from "@/components/ui/Tooltip";
import { Sidebar } from "./Sidebar";
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
});
