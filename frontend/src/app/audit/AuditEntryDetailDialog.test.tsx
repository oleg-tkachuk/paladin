import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

// The detail dialog re-reads the entry by id via auditClient.getAuditLogEntry.
const h = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { getAuditLogEntry: h.get },
}));

import { AuditEntryDetailDialog } from "./AuditEntryDetailDialog";

const enc = (o: unknown) => new TextEncoder().encode(JSON.stringify(o));

describe("AuditEntryDetailDialog", () => {
  beforeEach(() => h.get.mockReset());

  it("fetches the entry by id and renders decoded before/after snapshots", async () => {
    h.get.mockResolvedValue({
      entryId: "e1",
      at: { seconds: 1_700_000_000n },
      action: "admin.BucketService.UpdateBucket",
      actorSubject: "user:ops",
      actorTenantId: "t-1",
      resourceName: "buckets/b1",
      requestId: "req-abc",
      sourceIp: "10.0.0.1",
      capabilityId: "",
      errorMessage: "",
      beforeJson: enc({ versioning: false }),
      afterJson: enc({ versioning: true }),
    });

    render(<AuditEntryDetailDialog entryId="e1" onOpenChange={() => {}} />);

    // Fetched by id.
    expect(h.get).toHaveBeenCalledWith({ entryId: "e1" });

    // Action + a decoded snapshot value both render.
    expect(
      await screen.findByText("admin.BucketService.UpdateBucket"),
    ).toBeInTheDocument();
    expect(await screen.findByText(/"versioning": true/)).toBeInTheDocument();
    expect(screen.getByText(/"versioning": false/)).toBeInTheDocument();
  });

  it("does not fetch while closed (entryId null)", () => {
    render(<AuditEntryDetailDialog entryId={null} onOpenChange={() => {}} />);
    expect(h.get).not.toHaveBeenCalled();
  });

  it("notes when no state snapshot was recorded", async () => {
    h.get.mockResolvedValue({
      entryId: "e2",
      at: { seconds: 1_700_000_000n },
      action: "admin.BucketService.ListBuckets",
      actorSubject: "user:ops",
      actorTenantId: "",
      resourceName: "",
      requestId: "",
      sourceIp: "",
      capabilityId: "",
      errorMessage: "",
      beforeJson: new Uint8Array(),
      afterJson: new Uint8Array(),
    });

    render(<AuditEntryDetailDialog entryId="e2" onOpenChange={() => {}} />);
    expect(
      await screen.findByText(/no state snapshot recorded/i),
    ).toBeInTheDocument();
  });
});
