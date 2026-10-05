import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({
  useAuditLogs: vi.fn(() => ({
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
    expect(h.useAuditLogs).toHaveBeenCalledWith(expect.any(Number), "", "t-1");
  });
});
