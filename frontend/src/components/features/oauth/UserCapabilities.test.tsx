import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

const h = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userClient: { listUserCapabilities: h.list },
}));

import { UserCapability_State } from "@/gen/paladin/iam/v1/user_service_pb";

import { UserCapabilities } from "./UserCapabilities";

const USER = "tenants/t1/users/u1";
const ACTIVE = {
  id: "cap-1",
  clientId: "claude-agent",
  grantId: "grant-1",
  audience: ["paladin-data"],
  operations: ["read", "list"],
  issuedAt: { seconds: 1_700_000_000n },
  expiresAt: { seconds: 1_700_000_600n },
  state: UserCapability_State.ACTIVE,
  revocationReason: "",
};
const REVOKED = {
  ...ACTIVE,
  id: "cap-2",
  clientId: "old-agent",
  state: UserCapability_State.REVOKED,
  revocationReason: "an operator revoked the OAuth grant",
};

const row = (clientId: string) =>
  screen.findByRole("row", { name: new RegExp(clientId) });

describe("UserCapabilities", () => {
  beforeEach(() => {
    h.list.mockReset();
  });

  it("lists what acts for the user, the active ones first asked for", async () => {
    h.list.mockResolvedValue({ capabilities: [ACTIVE], nextPageToken: "" });
    render(<UserCapabilities userName={USER} own />);
    const r = await row(ACTIVE.clientId);
    expect(within(r).getByText("read, list")).toBeInTheDocument();
    expect(within(r).getByText("on paladin-data")).toBeInTheDocument();
    expect(h.list).toHaveBeenCalledWith(
      {
        name: USER,
        grantId: "",
        states: [UserCapability_State.ACTIVE],
        pageSize: 50,
        pageToken: "",
      },
      expect.anything(),
    );
  });

  it("says why a revoked one was revoked", async () => {
    h.list.mockResolvedValue({ capabilities: [REVOKED], nextPageToken: "" });
    render(<UserCapabilities userName={USER} own />);
    expect(
      within(await row(REVOKED.clientId)).getByText(REVOKED.revocationReason),
    ).toBeInTheDocument();
  });

  // The user's own grant opens on their applications page; an operator's on
  // the user's page, where the admin plane is theirs.
  it.each([
    [true, "/applications?apps.id=grant-1"],
    [false, "/users/t1/u1?apps.id=grant-1"],
  ])("links each to its grant (own: %s)", async (own, href) => {
    h.list.mockResolvedValue({ capabilities: [ACTIVE], nextPageToken: "" });
    render(<UserCapabilities userName={USER} own={own} />);
    const link = within(await row(ACTIVE.clientId)).getByRole("link", {
      name: "Open grant",
    });
    expect(link.getAttribute("href")).toBe(href);
  });

  it("narrows to the grant its link names, until told otherwise", async () => {
    window.history.replaceState(null, "", "/my-capabilities?grant=grant-1");
    h.list.mockResolvedValue({ capabilities: [ACTIVE], nextPageToken: "" });
    render(<UserCapabilities userName={USER} own />);
    await row(ACTIVE.clientId);
    expect(h.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ grantId: "grant-1" }),
      expect.anything(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Every grant" }));
    await waitFor(() =>
      expect(h.list).toHaveBeenLastCalledWith(
        expect.objectContaining({ grantId: "" }),
        expect.anything(),
      ),
    );
  });

  it("reads the next page on asking", async () => {
    h.list
      .mockResolvedValueOnce({ capabilities: [ACTIVE], nextPageToken: "p2" })
      .mockResolvedValueOnce({
        capabilities: [{ ...ACTIVE, id: "cap-3", clientId: "third-agent" }],
        nextPageToken: "",
      });
    render(<UserCapabilities userName={USER} own />);
    await row(ACTIVE.clientId);
    fireEvent.click(screen.getByRole("button", { name: "Show more" }));
    await row("third-agent");
    expect(h.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ pageToken: "p2" }),
      expect.anything(),
    );
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
  });

  it("says when the list cannot be read, and reads it again", async () => {
    h.list.mockRejectedValueOnce(new Error("unavailable"));
    h.list.mockResolvedValue({ capabilities: [ACTIVE], nextPageToken: "" });
    render(<UserCapabilities userName={USER} own />);
    fireEvent.click(await screen.findByRole("button", { name: /retry/i }));
    await row(ACTIVE.clientId);
  });
});
