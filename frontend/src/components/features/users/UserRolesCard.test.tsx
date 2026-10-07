import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ROLES } from "@/constants/roles";

const h = vi.hoisted(() => ({ updateUser: vi.fn(), onChanged: vi.fn() }));
vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({ busy: false, updateUser: h.updateUser }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import { UserRolesCard } from "./UserRolesCard";

const USER = {
  name: "tenants/t-1/users/u-1",
  roles: [ROLES.platformAdmin, ROLES.tenantUser],
  resourceVersion: "4",
};

const save = () => screen.getByRole("button", { name: "Save roles" });

beforeEach(() => {
  h.updateUser.mockReset().mockResolvedValue({ ok: true });
  h.onChanged.mockReset();
});

describe("UserRolesCard", () => {
  it("holds Save until something changes", () => {
    render(<UserRolesCard user={USER} onChanged={h.onChanged} />);
    expect(save()).toBeDisabled();
  });

  // platform.admin is not offered, so the console must not drop it either.
  it("keeps a role it does not offer when saving the others", async () => {
    render(<UserRolesCard user={USER} onChanged={h.onChanged} />);
    expect(screen.getByText(ROLES.platformAdmin)).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText(new RegExp(ROLES.bucketAdmin)));
    fireEvent.click(save());
    await waitFor(() =>
      expect(h.updateUser).toHaveBeenCalledWith({
        name: USER.name,
        resourceVersion: "4",
        roles: [ROLES.platformAdmin, ROLES.tenantUser, ROLES.bucketAdmin],
      }),
    );
    expect(h.onChanged).toHaveBeenCalled();
  });

  it("takes an offered role away", async () => {
    render(<UserRolesCard user={USER} onChanged={h.onChanged} />);
    fireEvent.click(screen.getByLabelText(new RegExp(ROLES.tenantUser)));
    fireEvent.click(save());
    await waitFor(() =>
      expect(h.updateUser).toHaveBeenCalledWith(
        expect.objectContaining({ roles: [ROLES.platformAdmin] }),
      ),
    );
  });
});
