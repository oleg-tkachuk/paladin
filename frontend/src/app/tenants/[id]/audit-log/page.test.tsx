import { describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { AuditLogEntrySchema } from "@/gen/paladin/admin/v1/types_pb";
import { fireEvent, render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({
  entries: [] as unknown[],
  useAuditLogs: vi.fn((..._args: unknown[]) => ({
    entries: h.entries,
    facetCounts: [],
    loading: false,
    error: null,
    nextCursor: "",
    refresh: vi.fn(),
    loadMore: vi.fn(),
  })),
}));
vi.mock("@/hooks/useAuditLogs", () => ({ useAuditLogs: h.useAuditLogs }));
vi.mock("../tenant-context", () => ({
  useTenantChangesBlocked: () => null,
  useTenant: () => ({ tenantId: "t-1", displayName: "Acme" }),
}));

vi.mock("@/components/features/audit/AuditEntryPanel", () => ({
  AUDIT_ENTRY_TABS: ["summary", "changes", "request"],
  AuditEntryPanel: ({ entryId }: { entryId: string | null }) =>
    entryId ? <div role="dialog">{entryId}</div> : null,
}));

import {
  AUDIT_FACET_OUTCOME,
  AUDIT_FACET_PLANE,
  AUDIT_FACET_SERVICE,
} from "@/lib/auditFacets";

import TenantAuditLogPage from "./page";

// The page reads the tenant's trail from the server rather than filtering a
// page of the whole log: a CEL filter applied after the fetch returned empty
// pages, and its resource-name prefix missed the rows of a collection, whose
// name nests the tenant under its bucket.
describe("TenantAuditLogPage", () => {
  it("asks for the tenant's trail, with no filter of its own", () => {
    render(<TenantAuditLogPage />);
    expect(
      screen.getByRole("heading", { name: /^Audit log$/ }),
    ).toBeInTheDocument();
    // With the facets, none chosen, which the server narrows and counts by.
    expect(h.useAuditLogs).toHaveBeenCalledWith(
      expect.any(Number),
      "",
      "t-1",
      { column: null, direction: null },
      {
        [AUDIT_FACET_PLANE]: [],
        [AUDIT_FACET_OUTCOME]: [],
        [AUDIT_FACET_SERVICE]: [],
      },
    );
  });

  // The trail loads a page at a time, so its headers ask the server for the
  // order; the third click hands it back to the server's own, newest first.
  it("asks the server for the order a header names", () => {
    render(<TenantAuditLogPage />);
    const action = screen.getByRole("button", { name: /^Action/ });
    const lastSort = () => h.useAuditLogs.mock.lastCall?.at(3);
    fireEvent.click(action);
    expect(lastSort()).toEqual({ column: "action", direction: "asc" });
    fireEvent.click(action);
    expect(lastSort()).toEqual({ column: "action", direction: "desc" });
    fireEvent.click(action);
    expect(lastSort()).toEqual({ column: null, direction: null });
  });
});

// A row opens its entry beside the trail, as on /audit, and the open entry
// is in the URL.
describe("TenantAuditLogPage rows", () => {
  it("opens an entry's details from its row", () => {
    window.history.replaceState(null, "", "/tenants/t-1/audit-log");
    h.entries = [
      create(AuditLogEntrySchema, {
        entryId: "entry-9",
        action: "admin.BucketService.UpdateBucket",
        actorSubject: "user:ops",
        resourceName: "buckets/b9",
        errorMessage: "",
      }),
    ];
    render(<TenantAuditLogPage />);
    const row = screen.getByRole("row", { name: /buckets\/b9/ });
    expect(row).toHaveAttribute("aria-haspopup", "dialog");
    fireEvent.click(row);
    expect(screen.getByRole("dialog")).toHaveTextContent("entry-9");
    expect(window.location.search).toBe("?id=entry-9");
    h.entries = [];
  });
});
