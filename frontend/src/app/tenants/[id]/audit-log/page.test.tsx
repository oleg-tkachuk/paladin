import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({
  useAuditLogs: vi.fn((..._args: unknown[]) => ({
    entries: [],
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
    expect(h.useAuditLogs).toHaveBeenCalledWith(expect.any(Number), "", "t-1", {
      column: null,
      direction: null,
    });
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
