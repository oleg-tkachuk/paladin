import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const CURRENT = "01a0f4a9-7600-77a9-862e-11ea31d523ca";
const OTHER = "01a0f4a9-7600-77a9-862e-22ea31d523cb";
const DISABLED = "01a0f4a9-7600-77a9-862e-33ea31d523cc";

const h = vi.hoisted(() => ({
  roles: ["platform.admin"] as string[],
  switchTenant: vi.fn(),
  push: vi.fn(),
  memberships: {
    memberships: [] as unknown[],
    loading: false,
    error: null as string | null,
    load: vi.fn(),
  },
}));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: h.push }) }));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: CURRENT, tenant: { displayName: "Platform" } }),
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    user: { roles: h.roles, tenantSlug: "platform" },
    switchTenant: h.switchTenant,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/hooks/useMemberships", () => ({
  useMemberships: () => h.memberships,
}));

import {
  MEMBERSHIPS_FAILED,
  TENANT_SWITCHER_LABEL,
  TenantSwitcher,
} from "./TenantSwitcher";

const membership = (tenantId: string, tenantSlug: string, extra = {}) => ({
  tenantId,
  tenantSlug,
  roles: ["tenant.admin"],
  disabled: false,
  current: false,
  ...extra,
});

beforeEach(() => {
  h.roles = ["platform.admin"];
  h.switchTenant.mockReset().mockResolvedValue(undefined);
  h.push.mockReset();
  h.memberships.load.mockReset();
  h.memberships.error = null;
  h.memberships.memberships = [
    membership(CURRENT, "platform", { current: true }),
    membership(OTHER, "acme"),
    membership(DISABLED, "gone", { disabled: true }),
  ];
});

async function openSwitcher() {
  const user = userEvent.setup();
  await user.click(
    screen.getByRole("button", {
      name: new RegExp(`^${TENANT_SWITCHER_LABEL}`),
    }),
  );
  return user;
}

describe("TenantSwitcher", () => {
  it("names the signed-in tenant on the trigger and in the list alike", async () => {
    render(<TenantSwitcher />);
    expect(
      screen.getByRole("button", {
        name: new RegExp(`^${TENANT_SWITCHER_LABEL}`),
      }),
    ).toHaveAccessibleName(`${TENANT_SWITCHER_LABEL} — signed in to Platform`);
    await openSwitcher();
    expect(h.memberships.load).toHaveBeenCalled();
    const current = screen.getByRole("option", { name: /Platform/ });
    expect(within(current).getByLabelText("Signed in")).toBeInTheDocument();
  });

  it("offers no backend or bucket to pick", async () => {
    render(<TenantSwitcher />);
    await openSwitcher();
    expect(screen.queryByText(/backend/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/bucket/i)).not.toBeInTheDocument();
  });

  it("switches to another membership", async () => {
    render(<TenantSwitcher />);
    const user = await openSwitcher();
    await user.click(screen.getByRole("option", { name: /acme/ }));
    expect(h.switchTenant).toHaveBeenCalledWith(OTHER);
  });

  it("does not switch to the signed-in tenant or a disabled one", async () => {
    render(<TenantSwitcher />);
    const user = await openSwitcher();
    await user.click(screen.getByRole("option", { name: /Platform/ }));
    await openSwitcher();
    await user.click(screen.getByRole("option", { name: /gone/ }));
    expect(h.switchTenant).not.toHaveBeenCalled();
  });

  it("says the list failed and offers a retry", async () => {
    h.memberships.memberships = [];
    h.memberships.error = "memberships failed (503)";
    render(<TenantSwitcher />);
    const user = await openSwitcher();
    expect(screen.getByRole("alert")).toHaveTextContent(MEMBERSHIPS_FAILED);
    h.memberships.load.mockClear();
    await user.click(screen.getByRole("option", { name: /Retry/ }));
    expect(h.memberships.load).toHaveBeenCalled();
  });

  it("links to the tenants page only for the admin plane", async () => {
    const { unmount } = render(<TenantSwitcher />);
    const user = await openSwitcher();
    await user.click(screen.getByRole("option", { name: /Manage tenants/ }));
    expect(h.push).toHaveBeenCalledWith("/tenants");
    unmount();

    h.roles = ["tenant.user"];
    render(<TenantSwitcher />);
    await openSwitcher();
    expect(
      screen.queryByRole("option", { name: /Manage tenants/ }),
    ).not.toBeInTheDocument();
  });
});
