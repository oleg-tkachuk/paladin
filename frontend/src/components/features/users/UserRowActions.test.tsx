import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Reset password ends the user's current password at once. It sat one click
// away on the row menu, beside a Delete that has always asked first.
const h = vi.hoisted(() => ({
  resetPassword: vi.fn(),
  onResetPassword: vi.fn(),
}));

vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({
    busy: false,
    updateUser: vi.fn(),
    deleteUser: vi.fn(),
    resetPassword: h.resetPassword,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import { UserRowActions } from "./UserRowActions";

const USER = {
  name: "tenants/t-1/users/u-1",
  subject: "alice",
  displayName: "Alice",
  disabled: false,
  resourceVersion: "3",
};

beforeEach(() => {
  h.resetPassword.mockReset();
  h.resetPassword.mockResolvedValue({ ok: true, password: "s3cret-once" });
  h.onResetPassword.mockReset();
});

async function chooseReset() {
  render(
    <UserRowActions
      user={USER}
      onChanged={vi.fn()}
      onResetPassword={h.onResetPassword}
    />,
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Actions for alice" }),
  );
  await userEvent.click(screen.getByText("Reset password"));
}

describe("UserRowActions reset password", () => {
  it("asks before resetting, and names what stops working", async () => {
    await chooseReset();
    expect(h.resetPassword).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      /Alice's current password stops working now/,
    );
  });

  it("resets once confirmed and hands over the new password", async () => {
    await chooseReset();
    await userEvent.click(
      screen.getByRole("button", { name: "Reset password" }),
    );
    await waitFor(() =>
      expect(h.resetPassword).toHaveBeenCalledWith(USER.name),
    );
    expect(h.onResetPassword).toHaveBeenCalledWith({
      subject: "alice",
      password: "s3cret-once",
    });
  });

  it("does nothing when the operator backs out", async () => {
    await chooseReset();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(h.resetPassword).not.toHaveBeenCalled();
  });
});
