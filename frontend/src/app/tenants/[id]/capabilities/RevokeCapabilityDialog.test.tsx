import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, typeToConfirm } from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

const h = vi.hoisted(() => ({
  revoke: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { revoke: h.revoke },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { RevokeCapabilityDialog } from "./RevokeCapabilityDialog";

const CAP = { id: "0f6c1d2e-0000-4000-8000-000000000001" } as Capability;

function open() {
  const onClose = vi.fn();
  const onRevoked = vi.fn();
  render(
    <RevokeCapabilityDialog
      cap={CAP}
      onClose={onClose}
      onRevoked={onRevoked}
    />,
  );
  return { onClose, onRevoked };
}

describe("RevokeCapabilityDialog", () => {
  beforeEach(() => {
    h.revoke.mockReset();
    h.revoke.mockResolvedValue({});
    h.showNotification.mockReset();
  });

  // Descendants stop verifying whether or not the revoke cascades: the
  // verifier walks the chain. The dialog must not suggest otherwise.
  it("warns that the delegations under it stop too, and that it is final", () => {
    open();
    const note = screen.getByRole("note");
    expect(note).toHaveTextContent(
      /Every delegation issued under it stops too/,
    );
    expect(note).toHaveTextContent(/cannot be undone/);
    expect(
      screen.getByRole("checkbox", { name: /audit trail/ }),
    ).not.toBeChecked();
  });

  it("revokes only once confirmed, with the reason and the cascade", async () => {
    const { onRevoked } = open();
    await userEvent.type(screen.getByLabelText(/Reason/), " leaked ");
    await userEvent.click(
      screen.getByRole("checkbox", { name: /audit trail/ }),
    );
    expect(h.revoke).not.toHaveBeenCalled();
    await typeToConfirm("revoke");
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith({
        id: CAP.id,
        reason: "leaked",
        cascadeChildren: true,
      }),
    );
    expect(onRevoked).toHaveBeenCalledOnce();
  });

  it("revokes nothing on cancel", async () => {
    const { onClose } = open();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalled();
    expect(h.revoke).not.toHaveBeenCalled();
  });

  // A second revoke keeps the first entry: the dialog says it was revoked
  // already, and when, rather than claim it revoked anything.
  it("says a capability was revoked already, and when", async () => {
    h.revoke.mockResolvedValue({
      alreadyRevoked: true,
      revocation: {
        revokedAt: timestampFromDate(new Date("2026-10-21T13:57:25Z")),
      },
    });
    open();
    await typeToConfirm("revoke");
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "info",
          title: "Already revoked",
          message: expect.stringContaining("that revocation stands"),
        }),
      ),
    );
  });

  it("says it revoked one that was not", async () => {
    h.revoke.mockResolvedValue({ alreadyRevoked: false });
    open();
    await typeToConfirm("revoke");
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({ type: "success", title: "Revoked" }),
      ),
    );
  });
});
