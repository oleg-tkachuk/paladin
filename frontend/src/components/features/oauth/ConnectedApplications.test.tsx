import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";
import userEvent from "@testing-library/user-event";

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

import { OAuthGrant_State } from "@/gen/paladin/iam/v1/user_service_pb";

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
  state: OAuthGrant_State.ACTIVE,
};
const WRITER = {
  ...GRANT,
  id: "0199-writer",
  clientId: "writer-agent",
  scopes: ["paladin.read", "paladin.write"],
};

const REVOKED = {
  ...GRANT,
  id: "0199-revoked",
  clientId: "revoked-agent",
  grantedAt: { seconds: 1_600_000_000n },
  state: OAuthGrant_State.REVOKED,
};
const EXPIRED = {
  ...GRANT,
  id: "0199-expired",
  clientId: "expired-agent",
  state: OAuthGrant_State.EXPIRED,
};

/** The application column, top to bottom. */
const shownClients = () =>
  screen
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0].textContent);

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
    // The state facet counts every state, so the ended grants come too.
    expect(h.list).toHaveBeenCalledWith(
      { name: USER, includeInactive: true },
      expect.anything(),
    );
  });

  it("shows the live grants first, and the ended ones under their state", async () => {
    h.list.mockResolvedValue({ grants: [GRANT, REVOKED, EXPIRED] });
    render(<ConnectedApplications userName={USER} own />);
    await row(GRANT.clientId);
    expect(shownClients()).toEqual([GRANT.clientId]);

    await userEvent.click(
      screen.getByRole("button", { name: "Filter by state, 1 chosen" }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Active/ }),
    );
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Revoked/ }),
    );
    await userEvent.keyboard("{Escape}");
    expect(await row(REVOKED.clientId)).toBeInTheDocument();
    expect(shownClients()).toEqual([REVOKED.clientId]);
    // An ended grant shows its state, and offers nothing to revoke.
    const revoked = await row(REVOKED.clientId);
    expect(within(revoked).getByText("revoked")).toBeInTheDocument();
    expect(
      within(revoked).queryByRole("button", { name: "Revoke" }),
    ).toBeNull();

    await userEvent.click(
      screen.getByRole("button", { name: "Filter by state, 1 chosen" }),
    );
    await userEvent.click(screen.getByRole("menuitem", { name: "Clear" }));
    await waitFor(() => expect(shownClients()).toHaveLength(3));
    expect(screen.getByText("3 shown")).toBeInTheDocument();
  });

  it("searches the application, plane and scopes", async () => {
    h.list.mockResolvedValue({ grants: [GRANT, WRITER] });
    render(<ConnectedApplications userName={USER} own />);
    await row(GRANT.clientId);
    const search = screen.getByRole("searchbox", {
      name: "Search connected applications",
    });
    fireEvent.change(search, { target: { value: "WRITE" } });
    expect(shownClients()).toEqual([WRITER.clientId]);
    expect(screen.getByText("1 of 2")).toBeInTheDocument();
    fireEvent.change(search, { target: { value: "nothing-like-it" } });
    expect(
      screen.getByText(/No application matches your filters\./),
    ).toBeInTheDocument();
  });

  it("sorts by a column, then the other way, then as listed", async () => {
    h.list.mockResolvedValue({ grants: [WRITER, GRANT] });
    render(<ConnectedApplications userName={USER} own />);
    await row(GRANT.clientId);
    const header = screen.getByRole("button", { name: /Application/ });
    fireEvent.click(header);
    expect(shownClients()).toEqual([GRANT.clientId, WRITER.clientId]);
    fireEvent.click(header);
    expect(shownClients()).toEqual([WRITER.clientId, GRANT.clientId]);
    fireEvent.click(header);
    expect(shownClients()).toEqual([WRITER.clientId, GRANT.clientId]);
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

  it("puts the open grant in the address, under the table's namespace", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    render(<ConnectedApplications userName={USER} own />);
    fireEvent.click(await row(GRANT.clientId));
    await screen.findByRole("dialog", { name: "Connected application" });
    expect(window.location.search).toBe(`?apps.id=${GRANT.id}`);
  });

  it("opens the grant a link names on load, an ended one too", async () => {
    h.list.mockResolvedValue({ grants: [GRANT, REVOKED] });
    window.history.replaceState(null, "", `/?apps.id=${REVOKED.id}`);
    render(<ConnectedApplications userName={USER} own />);
    const panel = await screen.findByRole("dialog", {
      name: "Connected application",
    });
    // The panel opens while the list is read, then shows the grant.
    await waitFor(() =>
      expect(panel).toHaveAccessibleDescription(REVOKED.clientId),
    );
    // An ended grant has nothing left to revoke.
    expect(
      within(panel).queryByRole("button", { name: "Revoke access" }),
    ).toBeNull();
  });

  it("says so when the linked grant is no longer on record", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    window.history.replaceState(null, "", "/?apps.id=0199-purged");
    render(<ConnectedApplications userName={USER} own />);
    expect(await screen.findByText(/No grant has this id/)).toBeInTheDocument();
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
