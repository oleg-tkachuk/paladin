import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

const h = vi.hoisted(() => ({ createUser: vi.fn() }));
vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({ busy: false, createUser: h.createUser }),
}));

import { OWN_TENANT_LABEL, UserCreateDialog } from "./UserCreateDialog";

const TENANTS = [{ tenantId: "t-2", slug: "acme", displayName: "Acme" }];

function show() {
  render(
    <UserCreateDialog
      isOpen
      onClose={vi.fn()}
      onCreated={vi.fn()}
      tenants={TENANTS}
    />,
  );
}

function fill() {
  fireEvent.change(screen.getByLabelText(/Subject/), {
    target: { value: "alice" },
  });
  fireEvent.change(screen.getByLabelText(/Initial password/), {
    target: { value: "a-long-enough-password" },
  });
}

const submitForm = () =>
  fireEvent.submit(
    screen.getByRole("button", { name: "Create user" }).closest("form")!,
  );

describe("UserCreateDialog", () => {
  beforeEach(() => {
    h.createUser.mockReset();
    h.createUser.mockResolvedValue({ ok: true });
  });

  // The empty option sends no parent, which CreateUser reads as the caller's
  // tenant; labelling it "no tenant" promised something the API never does.
  it("offers the caller's own tenant, and sends no parent for it", async () => {
    show();
    expect(screen.getByLabelText("Tenant")).toHaveTextContent(OWN_TENANT_LABEL);
    expect(screen.queryByText(/no tenant/i)).toBeNull();

    fill();
    submitForm();

    await waitFor(() => expect(h.createUser).toHaveBeenCalledTimes(1));
    expect(h.createUser.mock.calls[0][0].parent).toBe("");
  });

  it("sends the picked tenant as the parent", async () => {
    const user = userEvent.setup();
    show();
    await user.click(screen.getByLabelText("Tenant"));
    await user.click(screen.getByRole("option", { name: "Acme" }));
    fill();
    submitForm();

    await waitFor(() => expect(h.createUser).toHaveBeenCalledTimes(1));
    expect(h.createUser.mock.calls[0][0].parent).toBe("tenants/t-2");
  });

  it("says what the held submit is waiting for", () => {
    show();
    expect(
      screen.getByText("Enter a subject to continue."),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Subject/), {
      target: { value: "alice" },
    });
    expect(
      screen.getByText("The password needs at least 12 characters."),
    ).toBeInTheDocument();
  });

  it("sends the roles that are ticked", async () => {
    show();
    fill();
    fireEvent.click(screen.getByLabelText(/tenant\.admin/));
    submitForm();
    await waitFor(() => expect(h.createUser).toHaveBeenCalledTimes(1));
    expect(h.createUser.mock.calls[0][0].roles).toEqual([
      "tenant.user",
      "tenant.admin",
    ]);
  });

  it("says a role that grants nothing does so", () => {
    show();
    expect(screen.getByText(/Grants nothing yet/)).toBeInTheDocument();
  });

  // On a failed tenant read the select offered only the operator's own
  // tenant, which read as there being no other tenant to put a user in.
  // Creating in the own tenant still works, so it is disclosed, not blocked.
  it("discloses a failed tenant list without holding the own-tenant create", async () => {
    const retry = vi.fn();
    render(
      <UserCreateDialog
        isOpen
        onClose={vi.fn()}
        onCreated={vi.fn()}
        tenants={[]}
        tenantsFailed={{ reason: "unavailable: upstream", retry }}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Tenants could not be loaded/,
    );
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();

    fill();
    submitForm();
    await waitFor(() => expect(h.createUser).toHaveBeenCalledTimes(1));
  });
});
