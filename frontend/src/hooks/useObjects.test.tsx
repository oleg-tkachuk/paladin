import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const h = vi.hoisted(() => ({
  listObjects: vi.fn(async () => ({
    objects: [],
    page: { nextPageToken: "" },
  })),
}));
vi.mock("@/lib/connect/client", () => ({
  objectClient: { listObjects: h.listObjects },
  batchClient: {},
  presignClient: {},
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { tenantId: "t1" } }),
}));
vi.mock("@/context/RefreshContext", () => ({
  useRefreshSignal: () => 0,
  useBumpRefresh: () => vi.fn(),
}));

import { useObjects } from "./useObjects";

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

// The objects page re-registers its command-palette actions whenever refresh
// changes, and registering updates the actions context, which renders the page
// again. refresh depended on the whole query result — a new object on every
// render — so the page rendered without end ("Maximum update depth exceeded").
describe("useObjects", () => {
  it("keeps refresh the same function across renders", async () => {
    const { result, rerender } = renderHook(
      () => useObjects({ collection: "docs" }),
      { wrapper },
    );
    await waitFor(() => expect(h.listObjects).toHaveBeenCalled());
    const first = result.current.refresh;
    rerender();
    rerender();
    expect(result.current.refresh).toBe(first);
  });
});
