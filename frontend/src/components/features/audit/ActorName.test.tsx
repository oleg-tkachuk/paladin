import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({ getUser: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  userClient: { getUser: h.getUser },
}));

import { ActorName } from "./ActorName";

const USER = "01a0f2e5-67d5-7825-a9d9-7c7c87abff09";
const TENANT = "01a0f2e5-679a-73f8-beb4-2db3bf1e6e43";

describe("ActorName", () => {
  // A block, not an expression: mockReset returns the mock, and a function
  // returned from beforeEach is run as teardown — calling getUser once more.
  beforeEach(() => {
    h.getUser.mockReset();
  });

  it("shows a user's display name, keeping the id in the tooltip", async () => {
    h.getUser.mockResolvedValue({ displayName: "Olha", subject: "olha" });
    render(<ActorName subject={USER} tenantId={TENANT} />);
    const el = await screen.findByText("Olha");
    expect(el).toHaveAttribute("title", USER);
    expect(h.getUser).toHaveBeenCalledWith(
      { name: `tenants/${TENANT}/users/${USER}` },
      expect.anything(),
    );
  });

  it("falls back to the login subject when there is no display name", async () => {
    h.getUser.mockResolvedValue({ displayName: "", subject: "olha" });
    render(<ActorName subject={USER} tenantId={TENANT} />);
    expect(await screen.findByText("olha")).toBeInTheDocument();
  });

  it("shows the recorded id when the lookup is refused", async () => {
    h.getUser.mockRejectedValue(
      new ConnectError("denied", Code.PermissionDenied),
    );
    render(<ActorName subject={USER} tenantId={TENANT} />);
    // The id shows before the lookup too; wait for the refusal to land, so
    // this checks what is shown after it rather than before.
    await waitFor(() => expect(h.getUser).toHaveBeenCalledTimes(1));
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByText(USER)).toBeInTheDocument();
  });

  it("does not look up a subject that is not a user id", () => {
    render(<ActorName subject="system:bootstrap" tenantId={TENANT} />);
    expect(screen.getByText("system:bootstrap")).toBeInTheDocument();
    expect(h.getUser).not.toHaveBeenCalled();
  });

  it("does not look up without the actor's tenant", () => {
    render(<ActorName subject={USER} tenantId="" />);
    expect(screen.getByText(USER)).toBeInTheDocument();
    expect(h.getUser).not.toHaveBeenCalled();
  });

  it("shows the fallback for an entry with no actor", () => {
    render(<ActorName subject="" tenantId="" fallback="system" />);
    expect(screen.getByText("system")).toBeInTheDocument();
  });
});
