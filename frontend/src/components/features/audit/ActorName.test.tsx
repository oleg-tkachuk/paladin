import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({
  getUser: vi.fn(),
  getToken: vi.fn(),
  getCapability: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  userClient: { getUser: h.getUser },
  apiTokenClient: { get: h.getToken },
  capabilityClient: { get: h.getCapability },
}));

import { kNotFoundIsAnswer } from "@/lib/connect/expected";

import { ActorName } from "./ActorName";

const USER = "01a0f2e5-67d5-7825-a9d9-7c7c87abff09";
const TENANT = "01a0f2e5-679a-73f8-beb4-2db3bf1e6e43";

describe("ActorName", () => {
  // A block, not an expression: mockReset returns the mock, and a function
  // returned from beforeEach is run as teardown — calling getUser once more.
  beforeEach(() => {
    h.getUser.mockReset();
    h.getToken.mockReset();
    h.getCapability.mockReset();
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

  // A user deleted since the entry was written is a state the row shows,
  // not an RPC error to log on every audit page view.
  it("shows the recorded id for a user deleted since, without logging it", async () => {
    h.getUser.mockRejectedValue(new ConnectError("gone", Code.NotFound));
    render(<ActorName subject={USER} tenantId={TENANT} />);
    await waitFor(() => expect(h.getUser).toHaveBeenCalledTimes(1));
    const opts = h.getUser.mock.calls[0][1];
    expect(opts.contextValues.get(kNotFoundIsAnswer)).toBe(true);
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

// A token's or a capability's subject is its kind and its id; each is named
// as the operator knows it, with the recorded subject in the tooltip.
describe("ActorName for an automated actor", () => {
  const ID = "01a0f2e5-67d5-7825-a9d9-7c7c87abff10";

  beforeEach(() => {
    h.getUser.mockReset();
    h.getToken.mockReset();
    h.getCapability.mockReset();
  });

  it("names an API token by its display name", async () => {
    h.getToken.mockResolvedValue({
      apiToken: { displayName: "rag-api-issuer" },
    });
    render(<ActorName subject={`apikey:${ID}`} tenantId={TENANT} />);
    const el = await screen.findByText("rag-api-issuer");
    expect(el.closest("[title]")).toHaveAttribute("title", `apikey:${ID}`);
    expect(screen.getByText("token")).toBeInTheDocument();
    expect(h.getToken).toHaveBeenCalledWith(
      { name: `tenants/${TENANT}/apiTokens/${ID}` },
      expect.anything(),
    );
    expect(h.getUser).not.toHaveBeenCalled();
  });

  it("names a capability by the subject it was issued to", async () => {
    h.getCapability.mockResolvedValue({
      capability: { subject: { subject: "rag-worker" } },
    });
    render(<ActorName subject={`capability:${ID}`} tenantId={TENANT} />);
    expect(await screen.findByText("rag-worker")).toHaveAttribute(
      "title",
      `capability:${ID}`,
    );
    expect(h.getCapability).toHaveBeenCalledWith({ id: ID }, expect.anything());
  });

  it("shows the recorded subject when the token is gone", async () => {
    h.getToken.mockRejectedValue(new ConnectError("gone", Code.NotFound));
    render(<ActorName subject={`apikey:${ID}`} tenantId={TENANT} />);
    await waitFor(() => expect(h.getToken).toHaveBeenCalledTimes(1));
    expect(screen.getByText(`apikey:${ID}`)).toBeInTheDocument();
  });

  it("looks nothing up for a system actor", () => {
    render(<ActorName subject="system:bootstrap" tenantId={TENANT} />);
    expect(screen.getByText("system:bootstrap")).toBeInTheDocument();
    expect(h.getToken).not.toHaveBeenCalled();
    expect(h.getCapability).not.toHaveBeenCalled();
    expect(h.getUser).not.toHaveBeenCalled();
  });
});
