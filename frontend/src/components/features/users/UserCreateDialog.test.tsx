import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({ createUser: vi.fn() }));
vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({ busy: false, createUser: h.createUser }),
}));

import { OWN_TENANT_LABEL, UserCreateDialog } from "./UserCreateDialog";

const TENANTS = [{ tenantId: "t-2", slug: "acme", displayName: "Acme" }];

function fill() {
  fireEvent.change(screen.getByLabelText("Subject"), {
    target: { value: "alice" },
  });
  fireEvent.change(screen.getByLabelText("Initial password"), {
    target: { value: "a-long-enough-password" },
  });
}

describe("UserCreateDialog tenant", () => {
  beforeEach(() => {
    h.createUser.mockReset();
    h.createUser.mockResolvedValue({ ok: true });
  });

  // The empty option sends no parent, which CreateUser reads as the caller's
  // tenant; labelling it "no tenant" promised something the API never does.
  it("offers the caller's own tenant, and sends no parent for it", async () => {
    render(
      <UserCreateDialog
        isOpen
        onClose={vi.fn()}
        onCreated={vi.fn()}
        tenants={TENANTS}
      />,
    );
    const select = screen.getByLabelText("Tenant") as HTMLSelectElement;
    expect(select.selectedOptions[0].textContent).toBe(OWN_TENANT_LABEL);
    expect(screen.queryByText(/no tenant/i)).toBeNull();

    fill();
    fireEvent.click(screen.getByRole("button", { name: "Create user" }));

    await waitFor(() => expect(h.createUser).toHaveBeenCalledTimes(1));
    expect(h.createUser.mock.calls[0][0].parent).toBe("");
  });

  it("sends the picked tenant as the parent", async () => {
    render(
      <UserCreateDialog
        isOpen
        onClose={vi.fn()}
        onCreated={vi.fn()}
        tenants={TENANTS}
      />,
    );
    fireEvent.change(screen.getByLabelText("Tenant"), {
      target: { value: "t-2" },
    });
    fill();
    fireEvent.click(screen.getByRole("button", { name: "Create user" }));

    await waitFor(() => expect(h.createUser).toHaveBeenCalledTimes(1));
    expect(h.createUser.mock.calls[0][0].parent).toBe("tenants/t-2");
  });
});
