import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

const h = vi.hoisted(() => ({
  revoke: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  apiTokenClient: { revoke: h.revoke },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { RevokeTokenDialog } from "./RevokeTokenDialog";

const TOKEN = {
  name: "tenants/acme/apiTokens/t1",
  prefix: "ab12",
  displayName: "ci",
} as APIToken;

function open() {
  const onClose = vi.fn();
  const onRevoked = vi.fn();
  render(
    <RevokeTokenDialog token={TOKEN} onClose={onClose} onRevoked={onRevoked} />,
  );
  return { onClose, onRevoked };
}

describe("RevokeTokenDialog", () => {
  beforeEach(() => {
    h.revoke.mockReset();
    h.revoke.mockResolvedValue({});
    h.showNotification.mockReset();
  });

  it("names the token and warns the revoke is final", () => {
    open();
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      "paladin_pat_ab12…",
    );
    expect(screen.getByRole("note")).toHaveTextContent(/cannot be undone/);
  });

  it("revokes only once confirmed", async () => {
    const { onRevoked } = open();
    expect(h.revoke).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith({ name: TOKEN.name }),
    );
    expect(onRevoked).toHaveBeenCalledOnce();
  });

  it("revokes nothing on cancel", async () => {
    const { onClose } = open();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalled();
    expect(h.revoke).not.toHaveBeenCalled();
  });
});
