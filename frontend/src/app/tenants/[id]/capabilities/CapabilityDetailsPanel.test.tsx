import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

const h = vi.hoisted(() => ({
  get: vi.fn(),
  getToken: vi.fn(),
  revoke: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { get: h.get },
  apiTokenClient: { get: h.getToken },
}));
vi.mock("@/lib/utils", async (orig) => ({
  ...(await orig<typeof import("@/lib/utils")>()),
  copyToClipboard: vi.fn(async () => true),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";
import { CapabilityDetailsPanel } from "./CapabilityDetailsPanel";

const CAP = {
  id: "cap-1",
  issuer: "paladin",
  audience: ["data"],
  parentId: "",
  subject: { kind: PrincipalKind.AGENT, subject: "agent:1", tenantId: "t-1" },
  caveats: {
    ops: ["get"],
    requestCeiling: { case: "maxRequests", value: 0n },
    copyCeiling: { case: "maxCopies", value: 0n },
    budgetCeiling: {
      case: "unlimitedBudget",
      value: { currencyCode: "USD" },
    },
  },
} as unknown as Capability;

function open() {
  render(
    <CapabilityDetailsPanel
      id={CAP.id}
      cap={CAP}
      usageEntry="never"
      onClose={vi.fn()}
      onRevoke={h.revoke}
    />,
  );
}

describe("CapabilityDetailsPanel", () => {
  beforeEach(() => {
    h.get.mockReset();
  });

  it("reads the record for its issuer and revocation", async () => {
    h.get.mockResolvedValue({
      capability: CAP,
      issuedBy: { kind: PrincipalKind.USER, subject: "operator@example.com" },
      revocation: {
        revokedAt: timestampFromDate(new Date("2026-10-08T12:00:00Z")),
        reason: "leaked in a log",
        actor: "security",
        cascade: true,
      },
    });
    open();
    expect(
      await screen.findByText(/operator@example\.com/),
    ).toBeInTheDocument();
    expect(screen.getByText("leaked in a log")).toBeInTheDocument();
    expect(screen.getByText("security")).toBeInTheDocument();
    expect(
      screen.getByText(/with everything delegated from it/),
    ).toBeInTheDocument();
    expect(h.get).toHaveBeenCalledWith({ id: "cap-1" }, expect.anything());
  });

  // An API token is named as the console names it everywhere else, not by
  // the bare apikey:<id> it signs as.
  it("names an API token that issued or revoked the capability", async () => {
    const tenant = "01a0fcfc-281e-7c59-963c-8a946fe82a47";
    const actor = "apikey:c96e0f84-b32b-405d-81af-2a091f0faea6";
    h.getToken.mockResolvedValue({ apiToken: { displayName: "rag-api" } });
    const cap = {
      ...CAP,
      subject: { ...CAP.subject, tenantId: tenant },
    } as unknown as Capability;
    h.get.mockResolvedValue({
      capability: cap,
      issuedBy: { kind: PrincipalKind.USER, subject: actor, tenantId: tenant },
      revocation: { actor, reason: "run finished" },
    });
    render(
      <CapabilityDetailsPanel
        id={cap.id}
        cap={cap}
        usageEntry="never"
        onClose={vi.fn()}
        onRevoke={h.revoke}
      />,
    );
    await waitFor(() => expect(screen.getAllByText("rag-api")).toHaveLength(2));
    expect(screen.getAllByTitle(actor)).toHaveLength(2);
  });

  // The id is the panel's header; a row repeating it only pushed the rest
  // down, and the status leads.
  it("leads with the lifetime, the id only in the header", async () => {
    h.get.mockResolvedValue({ capability: CAP });
    open();
    await screen.findByRole("heading", { name: "Lifetime" });
    expect(screen.queryByText("Capability ID")).toBeNull();
    const sections = [...document.querySelectorAll("h3")].map(
      (e) => e.textContent,
    );
    expect(sections[0]).toBe("Lifetime");
  });

  // Opened by a link, the capability comes from Get, which names no
  // disposition: its own revocation says it is revoked, and there is nothing
  // left to revoke.
  it("reads a revoked capability as revoked, and offers no revoke", async () => {
    h.get.mockResolvedValue({
      capability: CAP,
      revocation: { actor: "security", reason: "leaked" },
    });
    open();
    expect(await screen.findByText("revoked")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Revoke/ })).toBeNull();
  });

  it("offers a revoke while the capability is not revoked", async () => {
    h.get.mockResolvedValue({ capability: CAP });
    open();
    expect(await screen.findByText("not revoked itself")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Revoke/ })).toBeInTheDocument();
  });

  it("says when the capability has no revocation of its own", async () => {
    h.get.mockResolvedValue({
      capability: CAP,
      issuedBy: { subject: "operator" },
    });
    open();
    expect(await screen.findByText("not revoked itself")).toBeInTheDocument();
  });

  // 0 is a limit that allows nothing, distinct from no limit at all.
  it("tells a limit of zero from an unlimited ceiling", async () => {
    h.get.mockResolvedValue({ capability: CAP });
    open();
    // Each label's <dd> is the element after its <dt>.
    const value = (label: string) =>
      screen.getByText(label).nextElementSibling?.textContent;
    expect(value("Max requests")).toBe("0");
    expect(value("Max Biscuit copies")).toBe("0");
    expect(value("Max budget")).toBe("unlimited");
  });

  it("marks the record unavailable when the read fails", async () => {
    h.get.mockRejectedValue(new Error("boom"));
    open();
    expect((await screen.findAllByText("unavailable")).length).toBeGreaterThan(
      0,
    );
  });

  // A link names a capability by id; the list the page browsed may not hold it.
  describe("by id alone", () => {
    const openById = () =>
      render(
        <CapabilityDetailsPanel
          id={CAP.id}
          cap={null}
          usageEntry={undefined}
          onClose={vi.fn()}
          onRevoke={h.revoke}
        />,
      );

    it("shows the capability its record holds", async () => {
      h.get.mockResolvedValue({ capability: CAP });
      openById();
      expect(screen.getByText("Reading the capability…")).toBeInTheDocument();
      expect(await screen.findByText(/agent:1/)).toBeInTheDocument();
    });

    it("says there is none when the record cannot be read", async () => {
      h.get.mockRejectedValue(new Error("not found"));
      openById();
      expect(
        await screen.findByText(/No capability cap-1 is on record/),
      ).toBeInTheDocument();
    });
  });

  it("stays closed with no id", () => {
    render(
      <CapabilityDetailsPanel
        id={null}
        cap={CAP}
        usageEntry="never"
        onClose={vi.fn()}
        onRevoke={h.revoke}
      />,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(h.get).not.toHaveBeenCalled();
  });

  // Confirmed on the button that copied, not by a toast.
  it("says Copied on the identifier's copy button", async () => {
    const user = userEvent.setup();
    h.get.mockResolvedValue({ capability: CAP });
    open();
    await user.click(
      await screen.findByRole("button", { name: "Copy capability details id" }),
    );
    expect(
      await screen.findByRole("button", { name: /Copied/ }),
    ).toBeInTheDocument();
    expect(h.showNotification).not.toHaveBeenCalled();
  });

  // Revoking is a decision, confirmed by the page's own dialog.
  it("asks the page to revoke the capability shown", async () => {
    const user = userEvent.setup();
    h.revoke.mockReset();
    h.get.mockResolvedValue({ capability: CAP });
    open();
    await user.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(h.revoke).toHaveBeenCalledWith(CAP);
  });
});
