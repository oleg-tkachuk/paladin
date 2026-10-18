import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({
  list: vi.fn(),
  revoke: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));
vi.mock("@/lib/connect/client", () => ({
  userClient: { listOAuthGrants: h.list, revokeOAuthGrant: h.revoke },
}));

import { ConnectedApplications } from "./ConnectedApplications";

const USER = "tenants/t1/users/u1";
const GRANT = {
  id: "0199-grant",
  clientId: "paladin-e2e-agent",
  audience: "paladin-mcp",
  scopes: ["paladin.read"],
  grantedAt: { seconds: 1_700_000_000n },
  refreshedAt: { seconds: 1_700_000_600n },
  expiresAt: { seconds: 1_700_604_800n },
};
const WRITER = {
  ...GRANT,
  id: "0199-writer",
  clientId: "writer-agent",
  scopes: ["paladin.read", "paladin.write"],
};

const row = (clientId: string) =>
  screen.findByRole("row", { name: new RegExp(clientId) });

describe("ConnectedApplications", () => {
  beforeEach(() => {
    h.list.mockReset();
    h.revoke.mockReset().mockResolvedValue({});
  });

  it("lists each application with whether it may write", async () => {
    h.list.mockResolvedValue({ grants: [GRANT, WRITER] });
    render(<ConnectedApplications userName={USER} own />);
    expect(
      within(await row(GRANT.clientId)).getByText("read only"),
    ).toBeInTheDocument();
    expect(
      within(await row(WRITER.clientId)).getByText("read & write"),
    ).toBeInTheDocument();
    expect(h.list).toHaveBeenCalledWith({ name: USER }, expect.anything());
  });

  it("opens an application's grant from its row, scopes spelled out", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    render(<ConnectedApplications userName={USER} own />);
    fireEvent.click(await row(GRANT.clientId));
    const dialog = await screen.findByRole("dialog", {
      name: "Connected application",
    });
    expect(dialog).toHaveAccessibleDescription(GRANT.clientId);
    expect(
      within(dialog).getByText("Read what your account can read"),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(GRANT.audience)).toBeInTheDocument();
  });

  it.each([
    [true, /You have not let any application in/],
    [false, /This user has not let any application in/],
  ])("says so when no application was let in (own: %s)", async (own, text) => {
    h.list.mockResolvedValue({ grants: [] });
    render(<ConnectedApplications userName={USER} own={own} />);
    expect(await screen.findByText(text)).toBeInTheDocument();
  });

  it("revokes from the row only after it is confirmed, leaving the record shut", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    render(<ConnectedApplications userName={USER} own />);
    fireEvent.click(
      within(await row(GRANT.clientId)).getByRole("button", { name: "Revoke" }),
    );
    // The row's button is the button's, not the row's: no record opens.
    expect(
      screen.queryByRole("dialog", { name: "Connected application" }),
    ).toBeNull();
    const confirm = await screen.findByRole("alertdialog");
    expect(h.revoke).not.toHaveBeenCalled();
    fireEvent.click(within(confirm).getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith({ name: USER, grantId: GRANT.id }),
    );
    // The list is read again, so the revoked grant leaves it.
    await waitFor(() => expect(h.list).toHaveBeenCalledTimes(2));
  });

  it("revokes from the record", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    render(<ConnectedApplications userName={USER} own />);
    fireEvent.click(await row(GRANT.clientId));
    const dialog = await screen.findByRole("dialog", {
      name: "Connected application",
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Revoke access" }),
    );
    const confirm = await screen.findByRole("alertdialog");
    fireEvent.click(within(confirm).getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith({ name: USER, grantId: GRANT.id }),
    );
  });
});
