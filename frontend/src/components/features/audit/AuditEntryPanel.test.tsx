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
  requestJson: enc({ name: "b1", versioning: true }),
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

  it("shows the request the action was made with, pretty-printed", async () => {
    h.get.mockResolvedValue(UPDATE);
    panel("e1", "request");
    expect(await screen.findByText(/"versioning": true/)).toBeInTheDocument();
    expect(screen.getByText(/not the resource's state/)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Request" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Snapshots" })).toBeNull();
  });

  it("shows the fields the action changed, before and after", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      stateBeforeJson: enc({ name: "tenants/t", display_name: "Old" }),
      stateAfterJson: enc({ name: "tenants/t", display_name: "New" }),
    });
    panel("e1", "changes");
    expect(await screen.findByText("display_name")).toBeInTheDocument();
    expect(screen.getByText('"Old"')).toBeInTheDocument();
    expect(screen.getByText('"New"')).toBeInTheDocument();
    expect(screen.queryByText("name")).toBeNull();
  });

  it("shows a resource made, and one removed, whole", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      stateAfterJson: enc({ slug: "made" }),
    });
    const { unmount } = panel("e1", "changes");
    expect(await screen.findByText("Created")).toBeInTheDocument();
    expect(screen.getByText(/"slug": "made"/)).toBeInTheDocument();
    unmount();
    h.get.mockResolvedValue({
      ...UPDATE,
      stateBeforeJson: enc({ slug: "gone" }),
    });
    panel("e2", "changes");
    expect(await screen.findByText("Removed")).toBeInTheDocument();
  });

  it("says when no state was recorded", async () => {
    h.get.mockResolvedValue(UPDATE);
    panel("e1", "changes");
    expect(await screen.findByText(/no state recorded/i)).toBeInTheDocument();
  });

  it("shows a request that is not JSON as it was stored", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      requestJson: new TextEncoder().encode("mode=b"),
    });
    panel("e1", "request");
    expect(await screen.findByText("mode=b")).toBeInTheDocument();
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
      requestJson: enc({ subject: resource }),
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

  it("notes when no request was recorded", async () => {
    h.get.mockResolvedValue({
      ...UPDATE,
      action: "admin.BucketService.ListBuckets",
      requestJson: new Uint8Array(),
    });
    panel("e2", "request");
    expect(await screen.findByText(/no request recorded/i)).toBeInTheDocument();
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

  it("shows the client the call came from, as it sent it", async () => {
    const ua = "paladin-sdk-go/0.97.0 gateway/1.4";
    h.get.mockResolvedValue({ ...UPDATE, userAgent: ua });
    panel("e6");
    expect(await screen.findByText("Client")).toBeInTheDocument();
    expect(screen.getByText(ua)).toBeInTheDocument();
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
