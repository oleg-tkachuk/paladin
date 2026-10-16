import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";

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

import { OAuthGrantsCard } from "./OAuthGrantsCard";

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

describe("OAuthGrantsCard", () => {
  beforeEach(() => {
    h.list.mockReset();
    h.revoke.mockReset();
  });

  it("lists each client with its audience and what it may do", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    render(<OAuthGrantsCard userName={USER} own />);
    expect(await screen.findByText(GRANT.clientId)).toBeInTheDocument();
    expect(screen.getByText("paladin-mcp")).toBeInTheDocument();
    expect(
      screen.getByText("Read what your account can read"),
    ).toBeInTheDocument();
    expect(h.list).toHaveBeenCalledWith({ name: USER }, expect.anything());
  });

  it("says so when no application was let in", async () => {
    h.list.mockResolvedValue({ grants: [] });
    render(<OAuthGrantsCard userName={USER} own={false} />);
    expect(
      await screen.findByText("This user has not let any application in."),
    ).toBeInTheDocument();
  });

  it("revokes a grant only after it is confirmed", async () => {
    h.list.mockResolvedValue({ grants: [GRANT] });
    h.revoke.mockResolvedValue({});
    render(<OAuthGrantsCard userName={USER} own />);

    fireEvent.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(h.revoke).not.toHaveBeenCalled();
    expect(await screen.findByText("Revoke access?")).toBeInTheDocument();

    const confirm = screen
      .getAllByRole("button", { name: "Revoke" })
      .find((b) => b.closest("[role=alertdialog]"));
    fireEvent.click(confirm!);
    await waitFor(() =>
      expect(h.revoke).toHaveBeenCalledWith({ name: USER, grantId: GRANT.id }),
    );
    // The list is read again, so the revoked grant leaves it.
    await waitFor(() => expect(h.list).toHaveBeenCalledTimes(2));
  });
});
