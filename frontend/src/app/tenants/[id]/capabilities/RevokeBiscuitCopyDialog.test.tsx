import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  revokeBiscuit: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { revokeBiscuit: h.revokeBiscuit },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { RevokeBiscuitCopyDialog } from "./RevokeBiscuitCopyDialog";

const BISCUIT = "En0KEwoEMTIzNBgDIgkKBwgKEgMYgAgSJAgAEiB";
const JWT = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.c2ln";

function open(onClose = vi.fn()) {
  render(<RevokeBiscuitCopyDialog open onClose={onClose} />);
  return onClose;
}
const tokenBox = () => screen.getByLabelText("Biscuit");
const submit = () => screen.getByRole("button", { name: "Revoke copy" });

describe("RevokeBiscuitCopyDialog", () => {
  beforeEach(() => {
    h.revokeBiscuit.mockReset();
    h.showNotification.mockReset();
  });

  it("holds the submit until a token is pasted", () => {
    open();
    expect(submit()).toBeDisabled();
    fireEvent.change(tokenBox(), { target: { value: "   " } });
    expect(submit()).toBeDisabled();
  });

  // A JWT has no copies; sending one would only come back InvalidArgument.
  it("refuses a JWT and says what to do instead", () => {
    open();
    fireEvent.change(tokenBox(), { target: { value: JWT } });
    expect(submit()).toBeDisabled();
    expect(
      screen.getByText(/revoke its capability instead/),
    ).toBeInTheDocument();
    expect(tokenBox()).toHaveAttribute("aria-invalid", "true");
  });

  it("revokes the pasted copy, trimmed, with the reason", async () => {
    h.revokeBiscuit.mockResolvedValue({ capabilityId: "cap-1" });
    const onClose = open();
    fireEvent.change(tokenBox(), { target: { value: `  ${BISCUIT}\n` } });
    fireEvent.change(screen.getByLabelText(/Reason/), {
      target: { value: " leaked " },
    });
    fireEvent.click(submit());
    await waitFor(() =>
      expect(h.revokeBiscuit).toHaveBeenCalledWith({
        token: BISCUIT,
        reason: "leaked",
      }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(h.showNotification).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "success",
        message: expect.stringContaining("cap-1"),
      }),
    );
  });

  it("reports a refusal from the server", async () => {
    h.revokeBiscuit.mockRejectedValue(new Error("token: invalid signature"));
    open();
    fireEvent.change(tokenBox(), { target: { value: BISCUIT } });
    fireEvent.click(submit());
    await waitFor(() =>
      expect(h.showNotification).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error", title: "Revoke failed" }),
      ),
    );
  });
});
