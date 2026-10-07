import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the cross-tenant collections page. Mock the three
// data hooks + scope + notifications and pin the shell, the on-mount fetch, and
// the provision dialog.
const h = vi.hoisted(() => ({
  fetchCollections: vi.fn(),
  createCollection: vi.fn(() => Promise.resolve()),
  deleteCollection: vi.fn(() => Promise.resolve()),
  fetchBuckets: vi.fn(() => Promise.resolve()),
  fetchBackends: vi.fn(),
  showNotification: vi.fn(),
  // The page renders ListLoadError; without `error` here that branch never
  // runs and the guard is untested. Same omission as /tenants had.
  error: null as string | null,
}));

vi.mock("@/hooks/useCollections", () => ({
  useCollections: () => ({
    collections: [],
    nextPageToken: "",
    loading: false,
    error: h.error,
    fetchCollections: h.fetchCollections,
    createCollection: h.createCollection,
    deleteCollection: h.deleteCollection,
  }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: [],
    loading: false,
    fetchBuckets: h.fetchBuckets,
  }),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({ backends: [], fetchBackends: h.fetchBackends }),
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1", tenant: { displayName: "Acme" } }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title: string;
    actions?: React.ReactNode;
  }) => (
    <div>
      <h1>{title}</h1>
      {actions}
    </div>
  ),
}));

import CollectionsPage from "./page";

beforeEach(() => {
  h.fetchCollections.mockClear();
  h.createCollection.mockClear();
  h.deleteCollection.mockClear();
  h.showNotification.mockClear();
});

describe("CollectionsPage", () => {
  it("renders the header and a create action", () => {
    render(<CollectionsPage />);
    expect(
      screen.getByRole("heading", { name: "Collections" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New Collection/i }),
    ).toBeInTheDocument();
  });

  it("fetches collections on mount", () => {
    render(<CollectionsPage />);
    expect(h.fetchCollections).toHaveBeenCalled();
  });

  it("opens the provision dialog from the header action", async () => {
    render(<CollectionsPage />);
    await userEvent.click(
      screen.getByRole("button", { name: /New Collection/i }),
    );
    expect(
      screen.getByRole("dialog", { name: "New Collection" }),
    ).toBeInTheDocument();
  });
});

// The search box on this page did not work.
//
// Its contents went straight into `filter`, which the server compiles as CEL,
// so typing "logs" sent the expression `logs` — an undeclared identifier — and
// the plane answered InvalidArgument. The list emptied and the operator saw a
// search that finds nothing. The tenant-scoped collections page carries a
// comment describing this exact failure; nobody carried the fix across.
describe("CollectionsPage search", () => {
  const typeInSearch = (value: string) =>
    act(() => {
      fireEvent.change(screen.getByPlaceholderText(/search/i), {
        target: { value },
      });
    });

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("sends a CEL expression, not the raw box contents", () => {
    render(<CollectionsPage />);
    h.fetchCollections.mockClear();

    typeInSearch("logs");
    act(() => {
      vi.advanceTimersByTime(400);
    });

    const sent = h.fetchCollections.mock.calls.map((c) => c[0]);
    // The regression: "logs" reaching the plane as a filter.
    expect(sent).not.toContain("logs");
    expect(sent.at(-1)).toBe('search.contains("logs")');
  });

  it("debounces instead of listing once per keystroke", () => {
    render(<CollectionsPage />);
    h.fetchCollections.mockClear();

    typeInSearch("l");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    typeInSearch("lo");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    typeInSearch("log");
    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(h.fetchCollections.mock.calls.filter((c) => c[0])).toHaveLength(0);

    act(() => {
      vi.advanceTimersByTime(400);
    });
    const filtered = h.fetchCollections.mock.calls.filter((c) => c[0]);
    expect(filtered).toHaveLength(1);
    expect(filtered[0][0]).toBe('search.contains("log")');
  });
});

// The ListLoadError branch, which existed and had never run.
describe("CollectionsPage load failure", () => {
  afterEach(() => {
    h.error = null;
  });

  it("says the list is unknown, not empty", () => {
    h.error = "unavailable: connection refused";
    render(<CollectionsPage />);
    expect(
      screen.getByText(
        /could not be loaded — this list is unknown, not empty/i,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("No Collections yet.")).not.toBeInTheDocument();
  });

  it("still says 'none yet' when the list really is empty", () => {
    render(<CollectionsPage />);
    expect(screen.getByText("No Collections yet.")).toBeInTheDocument();
  });
});
