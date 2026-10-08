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
    maxRequests: 0,
    maxBudget: { currencyCode: "USD", units: 0n, nanos: 0 },
  },
} as unknown as Capability;

function open() {
  render(
    <CapabilityDetailsDialog cap={CAP} usageEntry="never" onClose={vi.fn()} />,
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

  it("marks the record unavailable when the read fails", async () => {
    h.get.mockRejectedValue(new Error("boom"));
    open();
    expect((await screen.findAllByText("unavailable")).length).toBeGreaterThan(
      0,
    );
  });
});
