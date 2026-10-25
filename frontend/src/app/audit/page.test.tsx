import { create } from "@bufbuild/protobuf";
import { AuditLogEntrySchema } from "@/gen/paladin/admin/v1/types_pb";
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
vi.mock("@/components/features/audit/AuditEntryPanel", () => ({
  AUDIT_ENTRY_TABS: ["summary", "changes", "request"],
  AuditEntryPanel: ({
    entryId,
    tab,
  }: {
    entryId: string | null;
    tab: string;
  }) =>
    entryId ? (
      <div role="dialog">
        {entryId} on {tab}
      </div>
    ) : null,
}));

import { LINKED_ROW_ATTR } from "@/components/ui/table";

import AuditPage from "./page";

const SEARCH_BOX = "Filter by action, actor, resource, client, request id…";

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

  // The search lives in the URL, so a link to the log opens it filtered, and
  // the first request already carries the filter.
  it("opens with the search a link names", () => {
    window.history.replaceState(null, "", "/audit?q=paladin-mcp");
    render(<AuditPage />);
    expect(screen.getByPlaceholderText(SEARCH_BOX)).toHaveValue("paladin-mcp");
    expect(h.filters[0]).toBe('search.contains("paladin-mcp")');
  });
});

describe("AuditPage rows", () => {
  beforeEach(() => {
    h.search = "";
    window.history.replaceState(null, "", "/audit");
    h.entries = [
      create(AuditLogEntrySchema, {
        entryId: "entry-1",
        action: "admin.TenantService.DeleteTenant",
        actorSubject: "user:ops",
        resourceName: "tenants/t-1",
        errorMessage: "",
      }),
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

// The open entry is in the URL: an open entry's address is its link, and a
// link to one opens it with its row marked.
describe("AuditPage linked entry", () => {
  beforeEach(() => {
    h.entries = [
      create(AuditLogEntrySchema, {
        entryId: "entry-1",
        action: "admin.TenantService.DeleteTenant",
        actorSubject: "user:ops",
        resourceName: "tenants/t-1",
        errorMessage: "",
      }),
      create(AuditLogEntrySchema, {
        entryId: "entry-2",
        action: "admin.TenantService.CreateTenant",
        actorSubject: "user:ops",
        resourceName: "tenants/t-2",
        errorMessage: "",
      }),
    ];
  });

  it("opens the entry a link names, marking its row", () => {
    window.history.replaceState(null, "", "/audit?id=entry-2");
    h.search = "id=entry-2";
    render(<AuditPage />);
    expect(screen.getByRole("dialog")).toHaveTextContent("entry-2");
    const linked = screen.getByRole("row", { name: /tenants\/t-2/ });
    expect(linked).toHaveAttribute(LINKED_ROW_ATTR);
    expect(
      screen.getByRole("row", { name: /tenants\/t-1/ }),
    ).not.toHaveAttribute(LINKED_ROW_ATTR);
  });

  it("opens the entry on the tab a link names", () => {
    window.history.replaceState(null, "", "/audit?id=entry-1&tab=request");
    h.search = "id=entry-1&tab=request";
    render(<AuditPage />);
    expect(screen.getByRole("dialog")).toHaveTextContent("entry-1 on request");
  });

  it("puts an entry opened from its row in the address", () => {
    window.history.replaceState(null, "", "/audit");
    h.search = "";
    render(<AuditPage />);
    fireEvent.click(screen.getByRole("row", { name: /tenants\/t-1/ }));
    expect(window.location.search).toBe("?id=entry-1");
  });
});
