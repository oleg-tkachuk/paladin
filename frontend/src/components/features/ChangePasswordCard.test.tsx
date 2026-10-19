import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({ change: vi.fn(), notify: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  authClient: { changePassword: h.change },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import { ChangePasswordCard } from "./ChangePasswordCard";

const OLD = "the-current-one-2026";
const NEW = "a-longer-replacement-2026";

beforeEach(() => {
  h.change.mockReset().mockResolvedValue({});
  h.notify.mockReset();
});

const rule = (name: RegExp) =>
  within(screen.getByRole("list", { name: "Password requirements" })).getByText(
    name,
  );

async function fill(old: string, next: string, confirm: string) {
  if (old) await userEvent.type(screen.getByLabelText("Current password"), old);
  if (next)
    await userEvent.type(
      screen.getByLabelText("New password", { exact: true }),
      next,
    );
  if (confirm)
    await userEvent.type(
      screen.getByLabelText("Confirm new password"),
      confirm,
    );
}

describe("ChangePasswordCard", () => {
  it("ticks each rule as it is met, and only then submits", async () => {
    render(<ChangePasswordCard />);
    const submit = screen.getByRole("button", { name: "Change password" });
    expect(rule(/At least 12 characters/)).toHaveTextContent("(not yet)");
    await fill(OLD, NEW, NEW);
    expect(rule(/At least 12 characters/)).toHaveTextContent("(met)");
    expect(rule(/Not the current password/)).toHaveTextContent("(met)");
    expect(rule(/Typed the same twice/)).toHaveTextContent("(met)");
    expect(submit).toBeEnabled();
    await userEvent.click(submit);
    await waitFor(() =>
      expect(h.change).toHaveBeenCalledWith({
        oldPassword: OLD,
        newPassword: NEW,
      }),
    );
  });

  it.each([
    ["too short", OLD, "short", "short"],
    ["the same as the current one", OLD, OLD, OLD],
    ["not confirmed", OLD, NEW, "something-else-2026"],
    ["no current password", "", NEW, NEW],
  ])("refuses a new password %s", async (_name, old, next, confirm) => {
    render(<ChangePasswordCard />);
    await fill(old, next, confirm);
    expect(
      screen.getByRole("button", { name: "Change password" }),
    ).toBeDisabled();
  });

  it("says why the server refused", async () => {
    h.change.mockRejectedValue(new Error("current password is wrong"));
    render(<ChangePasswordCard />);
    await fill(OLD, NEW, NEW);
    await userEvent.click(
      screen.getByRole("button", { name: "Change password" }),
    );
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ message: expect.stringContaining("wrong") }),
      ),
    );
  });
});
