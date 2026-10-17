import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  search: "",
  filters: [] as string[],
  entries: [] as unknown[],
}));

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(h.search),
}));
vi.mock("@/hooks/useAuditLogs", () => ({
  useAuditLogs: (_pageSize: number, filter: string) => (
    h.filters.push(filter),
    {
      entries: h.entries,
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

vi.mock("@/components/features/audit/ActorName", () => ({
  ActorName: ({ subject }: { subject: string }) => subject,
}));
vi.mock("./AuditEntryDetailDialog", () => ({
  AuditEntryDetailDialog: ({ entryId }: { entryId: string | null }) =>
    entryId ? <div role="dialog">{entryId}</div> : null,
}));

import AuditPage from "./page";

const SEARCH_BOX = "Filter by action, actor, resource, request id…";

// The search is the server's filter, so Load more pages on through the
// matches instead of through entries the box then hides.
describe("AuditPage search", () => {
  beforeEach(() => {
    h.search = "";
    h.filters = [];
    h.entries = [];
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

describe("AuditPage rows", () => {
  beforeEach(() => {
    h.search = "";
    h.entries = [
      {
        entryId: "entry-1",
        action: "admin.TenantService.DeleteTenant",
        actorSubject: "user:ops",
        resourceName: "tenants/t-1",
        errorMessage: "",
      },
    ];
  });

  it.each([
    ["a click", (row: HTMLElement) => fireEvent.click(row)],
    ["Enter", (row: HTMLElement) => fireEvent.keyDown(row, { key: "Enter" })],
  ])("opens an entry's record on %s", (_, act) => {
    render(<AuditPage />);
    const row = screen.getByRole("row", { name: /tenants\/t-1/ });
    expect(row).toHaveAttribute("aria-haspopup", "dialog");
    act(row);
    expect(screen.getByRole("dialog")).toHaveTextContent("entry-1");
  });
});
