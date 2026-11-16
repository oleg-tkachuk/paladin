import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { typeToConfirm } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// Reset password ends the user's current password at once. It sat one click
// away on the row menu, beside a Delete that has always asked first.
const h = vi.hoisted(() => ({
  updateUser: vi.fn(),
  onChanged: vi.fn(),
  resetPassword: vi.fn(),
  deleteUser: vi.fn(),
  onDeleted: vi.fn(),
  onResetPassword: vi.fn(),
}));

vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({
    busy: false,
    updateUser: h.updateUser,
    deleteUser: h.deleteUser,
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
  h.deleteUser.mockReset();
  h.deleteUser.mockResolvedValue({ ok: true });
  h.onDeleted.mockReset();
  h.updateUser.mockReset();
  h.updateUser.mockResolvedValue({ ok: true });
  h.onChanged.mockReset();
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

async function chooseDelete() {
  render(
    <UserRowActions
      user={USER}
      onChanged={vi.fn()}
      onDeleted={h.onDeleted}
      onResetPassword={h.onResetPassword}
    />,
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Actions for alice" }),
  );
  await userEvent.click(screen.getByText("Delete"));
}

// A user has no trash, so the login is typed before the delete goes.
describe("UserRowActions delete", () => {
  it("deletes only once the login is typed", async () => {
    await chooseDelete();
    const del = screen.getByRole("button", { name: "Delete" });
    expect(del).toBeDisabled();
    await typeToConfirm("alice");
    await userEvent.click(del);
    await waitFor(() =>
      expect(h.deleteUser).toHaveBeenCalledWith({
        name: USER.name,
        resourceVersion: "3",
      }),
    );
    expect(h.onDeleted).toHaveBeenCalledOnce();
  });

  it("keeps the dialog open with the reason when the delete is refused", async () => {
    h.deleteUser.mockResolvedValue({ ok: false, error: "changed elsewhere" });
    await chooseDelete();
    await typeToConfirm("alice");
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "changed elsewhere",
    );
    expect(h.onDeleted).not.toHaveBeenCalled();
  });
});

async function chooseToggle(user: typeof USER, item: "Disable" | "Enable") {
  render(
    <UserRowActions
      user={user}
      onChanged={h.onChanged}
      onResetPassword={h.onResetPassword}
    />,
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Actions for alice" }),
  );
  await userEvent.click(screen.getByText(item));
}

// Disabling cuts a user off: no new sign-in, and the console session ends on
// its next request. It sat one click away on the row menu, beside a Reset
// and a Delete that ask first. Enabling takes nothing away, so it does not ask.
describe("UserRowActions disable", () => {
  it("asks before disabling, and says what the user loses", async () => {
    await chooseToggle(USER, "Disable");
    expect(h.updateUser).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      /Alice can no longer sign in/,
    );
  });

  it("disables once confirmed", async () => {
    await chooseToggle(USER, "Disable");
    await userEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() =>
      expect(h.updateUser).toHaveBeenCalledWith({
        name: USER.name,
        resourceVersion: "3",
        disabled: true,
      }),
    );
    expect(h.onChanged).toHaveBeenCalledOnce();
  });

  it("does nothing when the operator backs out", async () => {
    await chooseToggle(USER, "Disable");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(h.updateUser).not.toHaveBeenCalled();
  });

  it("keeps the dialog open with the reason when the disable is refused", async () => {
    h.updateUser.mockResolvedValue({ ok: false, error: "changed elsewhere" });
    await chooseToggle(USER, "Disable");
    await userEvent.click(screen.getByRole("button", { name: "Disable" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "changed elsewhere",
    );
    expect(h.onChanged).not.toHaveBeenCalled();
  });

  it("enables at once: it takes nothing away", async () => {
    await chooseToggle({ ...USER, disabled: true }, "Enable");
    expect(screen.queryByRole("alertdialog")).toBeNull();
    await waitFor(() =>
      expect(h.updateUser).toHaveBeenCalledWith({
        name: USER.name,
        resourceVersion: "3",
        disabled: false,
      }),
    );
    expect(h.onChanged).toHaveBeenCalledOnce();
  });
});
