import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const h = vi.hoisted(() => ({
  listAuditLog: vi.fn(
    async (_req: Record<string, unknown>, _opts?: unknown) => ({
      entries: [],
      page: { nextPageToken: "" },
    }),
  ),
}));
vi.mock("@/lib/connect/client", () => ({
  auditClient: { listAuditLog: h.listAuditLog },
}));

import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import type { AuditOrderField } from "@/lib/auditOrder";

import type { SortState } from "./useTableSort";
import { useAuditLogs } from "./useAuditLogs";

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

// A tenant's trail is selected by the server. Filtering a page of the whole
// log after the fetch handed back empty pages with a cursor, and missed the
// tenant's own rows whose resource name did not start with its prefix.
describe("useAuditLogs", () => {
  it("asks the server for the tenant's trail", async () => {
    h.listAuditLog.mockClear();
    renderHook(() => useAuditLogs(10, "", "tenant-1"), { wrapper });
    await waitFor(() => expect(h.listAuditLog).toHaveBeenCalled());
    expect(h.listAuditLog).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "tenant-1", filter: "" }),
      expect.anything(),
    );
  });

  it("lists every tenant when none is named", async () => {
    h.listAuditLog.mockClear();
    renderHook(() => useAuditLogs(10), { wrapper });
    await waitFor(() => expect(h.listAuditLog).toHaveBeenCalled());
    expect(h.listAuditLog).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "" }),
      expect.anything(),
    );
  });
});

describe("useAuditLogs sort", () => {
  it("asks for the order and direction a sort names, and none without one", async () => {
    h.listAuditLog.mockClear();
    const { result, rerender } = renderHook(
      ({ sort }) => useAuditLogs(50, "", "", sort),
      {
        wrapper,
        initialProps: {
          sort: { column: null, direction: null } as SortState<AuditOrderField>,
        },
      },
    );
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(h.listAuditLog.mock.lastCall?.[0]).toMatchObject({
      orderBy: "",
      sortOrder: SortOrder.UNSPECIFIED,
    });

    rerender({ sort: { column: "resource_name", direction: "desc" } });
    await waitFor(() =>
      expect(h.listAuditLog.mock.lastCall?.[0]).toMatchObject({
        orderBy: "resource_name",
        sortOrder: SortOrder.DESC,
        // A new order starts from the first page, not an old cursor.
        page: { pageToken: "" },
      }),
    );
  });
});
