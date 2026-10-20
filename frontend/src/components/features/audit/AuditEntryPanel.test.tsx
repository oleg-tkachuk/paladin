import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

// The panel re-reads the entry by id via auditClient.getAuditLogEntry.
const h = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { getAuditLogEntry: h.get },
  userSettingsClient: { getMine: async () => ({ preferences: {} }) },
}));

import { AuditEntryPanel, type AuditEntryTab } from "./AuditEntryPanel";

const enc = (o: unknown) => new TextEncoder().encode(JSON.stringify(o));

function panel(entryId: string | null, tab: AuditEntryTab = "summary") {
  return render(
    <AuditEntryPanel
      entryId={entryId}
      tab={tab}
      onTabChange={() => {}}
      onClose={() => {}}
    />,
  );
}

const UPDATE = {
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
  beforeJson: enc({ name: "b1", versioning: false }),
  afterJson: enc({ name: "b1", versioning: true }),
};

describe("AuditEntryPanel", () => {
  beforeEach(() => {
    h.get.mockReset();
  });

  it("fetches the entry by id and shows its fields", async () => {
    h.get.mockResolvedValue(UPDATE);
    panel("e1");
    expect(h.get).toHaveBeenCalledWith({ entryId: "e1" });
    expect(
      await screen.findByText("admin.BucketService.UpdateBucket"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Audit entry" }),
    ).toHaveTextContent("e1");
  });

  it("shows what the action changed as a diff of the snapshots", async () => {
    h.get.mockResolvedValue(UPDATE);
    panel("e1", "changes");
    const diff = await screen.findByRole("table", {
      name: "Changes from before to after",
    });
    const removed = diff.querySelector('[data-diff="remove"]');
    const added = diff.querySelector('[data-diff="add"]');
    expect(removed).toHaveTextContent('"versioning": false');
    expect(added).toHaveTextContent('"versioning": true');
    // The unchanged field is context, not a change.
    expect(screen.getByText("1 added, 1 removed")).toBeInTheDocument();
  });

  it("reads a create, with no before, as all added", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      beforeJson: new Uint8Array(),
      afterJson: enc({ name: "b1" }),
    });
    panel("e1", "changes");
    await screen.findByRole("table");
    expect(document.querySelector('[data-diff="remove"]')).toBeNull();
    expect(screen.getByText("3 added, 0 removed")).toBeInTheDocument();
  });

  it("shows the decoded snapshots whole", async () => {
    h.get.mockResolvedValue(UPDATE);
    panel("e1", "snapshots");
    expect(await screen.findByText(/"versioning": true/)).toBeInTheDocument();
    expect(screen.getByText(/"versioning": false/)).toBeInTheDocument();
  });

  it("wraps identifiers and the action instead of overflowing the panel", async () => {
    const action = "/paladin.admin.v1.CapabilityService/Issue";
    const resource =
      "tenants/01a120f8-26c4-71fb-b70f-08579e18a292/capabilities/b907cb1f";
    h.get.mockResolvedValue({
      ...UPDATE,
      entryId: "e3",
      action,
      resourceName: resource,
      beforeJson: new Uint8Array(),
      afterJson: enc({ subject: resource }),
    });
    panel("e3");
    expect(await screen.findByText(action)).toHaveClass("break-all");
    expect(screen.getByText(resource)).toHaveClass("break-all");
  });

  it("does not fetch while closed (entryId null)", () => {
    panel(null);
    expect(h.get).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("notes when no state snapshot was recorded", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      action: "admin.BucketService.ListBuckets",
      beforeJson: new Uint8Array(),
      afterJson: new Uint8Array(),
    });
    panel("e2", "changes");
    expect(
      await screen.findByText(/no state snapshot recorded/i),
    ).toBeInTheDocument();
  });

  it("names the client that relayed the call", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      relayedBy: "paladin-mcp-server",
    });
    panel("e4");
    expect(await screen.findByText("Relayed by")).toBeInTheDocument();
    expect(screen.getByText("paladin-mcp-server")).toBeInTheDocument();
  });

  it("says why the entry could not be read", async () => {
    h.get.mockRejectedValue(new Error("not found"));
    panel("e5");
    expect(await screen.findByText(/not found/)).toBeInTheDocument();
  });
});

// The actor's id is shown beside the name only when a name was looked up for
// it: a system actor is its own name, and twice read as a mistake.
describe("AuditEntryPanel actor", () => {
  const entry = (actorSubject: string) => ({
    entryId: "e1",
    at: { seconds: 1_700_000_000n },
    action: "iam.bootstrap_backend.noop",
    actorSubject,
    actorTenantId: "",
    resourceName: "storageBackends/primary",
  });

  it("shows a system actor once", async () => {
    h.get.mockReset();
    h.get.mockResolvedValue(entry("system:bootstrap"));
    panel("e1");
    expect(await screen.findAllByText("system:bootstrap")).toHaveLength(1);
    expect(screen.queryByText("Actor id")).toBeNull();
  });

  it("keeps a user's id beside their name", async () => {
    const id = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
    h.get.mockReset();
    h.get.mockResolvedValue(entry(id));
    panel("e1");
    expect(await screen.findByText("Actor id")).toBeInTheDocument();
  });
});
