import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  search: "",
  filters: [] as string[],
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(h.search),
}));
vi.mock("@/hooks/useAuditLogs", () => ({
  useAuditLogs: (_pageSize: number, filter: string) => (
    h.filters.push(filter),
    {
      entries: [],
      loading: false,
      error: null,
      nextCursor: "",
      refresh: vi.fn(),
      loadMore: vi.fn(),
    }
  ),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/hooks/useAuditStream", () => ({
  useAuditStream: () => ({ connected: false }),
}));

import AuditPage from "./page";

const SEARCH_BOX = "Filter by action, actor, resource, request id…";

// The search is the server's filter, so Load more pages on through the
// matches instead of through entries the box then hides.
describe("AuditPage search", () => {
  beforeEach(() => {
    h.search = "";
    h.filters = [];
  });

  it("reads the log unfiltered with an empty box", () => {
    render(<AuditPage />);
    expect(h.filters.at(-1)).toBe("");
  });

  it("sends the typed text to the server, folded", async () => {
    render(<AuditPage />);
    fireEvent.change(screen.getByPlaceholderText(SEARCH_BOX), {
      target: { value: "DeleteTenant" },
    });
    await waitFor(() =>
      expect(h.filters.at(-1)).toBe('search.contains("deletetenant")'),
    );
  });

  it("presets the filter from an ?audience= deep link", () => {
    h.search = "audience=paladin-mcp";
    render(<AuditPage />);
    expect(h.filters[0]).toBe('search.contains("paladin-mcp")');
  });
});
