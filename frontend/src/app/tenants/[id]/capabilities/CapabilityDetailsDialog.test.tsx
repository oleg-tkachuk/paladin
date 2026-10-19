import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

const h = vi.hoisted(() => ({
  get: vi.fn(),
  showNotification: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { get: h.get },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";
import { CapabilityDetailsDialog } from "./CapabilityDetailsDialog";

const CAP = {
  id: "cap-1",
  issuer: "paladin",
  audience: ["data"],
  generation: 1n,
  parentId: "",
  subject: { kind: PrincipalKind.AGENT, subject: "agent:1", tenantId: "t-1" },
  caveats: {
    ops: ["get"],
    requestCeiling: { case: "maxRequests", value: 0n },
    budgetCeiling: {
      case: "unlimitedBudget",
      value: { currencyCode: "USD" },
    },
  },
} as unknown as Capability;

function open() {
  render(
    <CapabilityDetailsDialog
      id={CAP.id}
      cap={CAP}
      usageEntry="never"
      onClose={vi.fn()}
    />,
  );
}

describe("CapabilityDetailsDialog", () => {
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
        <CapabilityDetailsDialog
          id={CAP.id}
          cap={null}
          usageEntry={undefined}
          onClose={vi.fn()}
        />,
      );

    it("shows the capability its record holds", async () => {
      h.get.mockResolvedValue({ capability: CAP });
      openById();
      expect(screen.getByText("Reading the capability…")).toBeInTheDocument();
      expect(await screen.findByText("Copy ID")).toBeInTheDocument();
      expect(screen.getByText(/agent:1/)).toBeInTheDocument();
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
      <CapabilityDetailsDialog
        id={null}
        cap={CAP}
        usageEntry="never"
        onClose={vi.fn()}
      />,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(h.get).not.toHaveBeenCalled();
  });
});
