import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({
  pathname: "/tenants",
  user: { roles: ["tenant.user"] } as { roles: string[] } | null,
}));
vi.mock("next/navigation", () => ({ usePathname: () => h.pathname }));
vi.mock("@/context/AuthContext", () => ({ useAuth: () => ({ user: h.user }) }));

import { AdminPlaneGate } from "./AdminPlaneGate";
import { NO_ADMIN_ROLE_TITLE } from "./NoAdminRole";

function show() {
  render(
    <AdminPlaneGate>
      <div>page</div>
    </AdminPlaneGate>,
  );
}

describe("AdminPlaneGate", () => {
  beforeEach(() => {
    h.pathname = "/tenants";
    h.user = { roles: ["tenant.user"] };
  });

  it("stands in for an admin view a tenant.user reached by URL", () => {
    show();
    expect(screen.getByText(NO_ADMIN_ROLE_TITLE)).toBeInTheDocument();
    expect(screen.queryByText("page")).toBeNull();
  });

  it("lets a tenant.user through to a view that needs no admin plane", () => {
    h.pathname = "/profile";
    show();
    expect(screen.getByText("page")).toBeInTheDocument();
  });

  it("lets an admin-audience role through", () => {
    h.user = { roles: ["platform.tenant-provisioner"] };
    show();
    expect(screen.getByText("page")).toBeInTheDocument();
  });

  it("renders the page until the user is known", () => {
    h.user = null;
    show();
    expect(screen.getByText("page")).toBeInTheDocument();
  });
});
