import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({ revokeRun: vi.fn(), notify: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { revokeRun: h.revokeRun },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import { RevokeRunDialog, noticeOfRevokeRun } from "./RevokeRunDialog";

describe("noticeOfRevokeRun", () => {
  it("counts what it revoked", () => {
    expect(noticeOfRevokeRun("r-1", 3n)).toMatchObject({
      type: "success",
      message: "3 capabilities of run r-1 revoked.",
    });
    expect(noticeOfRevokeRun("r-1", 1n).message).toBe(
      "1 capability of run r-1 revoked.",
    );
  });

  // A repeat stops nothing new, and says so rather than claim it did.
  it("says when nothing was left", () => {
    expect(noticeOfRevokeRun("r-1", 0n)).toMatchObject({
      type: "info",
      title: "Nothing left to revoke",
    });
  });
});

describe("RevokeRunDialog", () => {
  beforeEach(() => {
    h.revokeRun.mockReset().mockResolvedValue({ revoked: 2n });
    h.notify.mockReset();
  });

  it("revokes the run in its tenant once confirmed, with the reason given", async () => {
    const onRevoked = vi.fn();
    const onClose = vi.fn();
    render(
      <RevokeRunDialog
        tenantId="t-1"
        run="r-1"
        onClose={onClose}
        onRevoked={onRevoked}
      />,
    );
    await userEvent.type(screen.getByLabelText("Reason (optional)"), "runaway");
    expect(screen.getByRole("button", { name: "Revoke run" })).toBeDisabled();
    await userEvent.type(
      screen.getByRole("textbox", { name: /type/i }),
      "revoke",
    );
    await userEvent.click(screen.getByRole("button", { name: "Revoke run" }));
    await waitFor(() =>
      expect(h.revokeRun).toHaveBeenCalledWith({
        tenantId: "t-1",
        runId: "r-1",
        reason: "runaway",
      }),
    );
    expect(onRevoked).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
    expect(h.notify).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Run revoked" }),
    );
  });

  it("says why the run was not revoked", async () => {
    h.revokeRun.mockRejectedValue(new Error("permission denied"));
    render(
      <RevokeRunDialog
        tenantId="t-1"
        run="r-1"
        onClose={vi.fn()}
        onRevoked={vi.fn()}
      />,
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: /type/i }),
      "revoke",
    );
    await userEvent.click(screen.getByRole("button", { name: "Revoke run" }));
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error", title: "Revoke failed" }),
      ),
    );
  });
});
