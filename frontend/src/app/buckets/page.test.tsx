import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

// Monolith list-page smoke pattern: mock the data hooks + the shell pieces
// (PageHeader pulls scope context) and assert the three render branches —
// loading, empty, and a populated row. This is the regression net that
// guards decomposing this page later.
const h = vi.hoisted(() => ({
  buckets: {
    buckets: [] as Array<Record<string, unknown>>,
    loading: false,
    fetchBuckets: vi.fn(),
    createBucket: vi.fn(),
    deleteBucket: vi.fn(),
  },
  backends: { backends: [] as Array<Record<string, unknown>> },
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/buckets",
  useSearchParams: () => new URLSearchParams(""),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/hooks/useBuckets", () => ({ useBuckets: () => h.buckets }));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));

import BucketsPage from "./page";
import { PROVISION_POLL_MS, PROVISION_STATE } from "@/lib/bucketProvision";

beforeEach(() => {
  h.buckets.buckets = [];
  h.buckets.loading = false;
  h.backends.backends = [];
});

describe("BucketsPage", () => {
  it("shows the empty state when there are no buckets", () => {
    render(<BucketsPage />);
    expect(screen.getByText("No buckets yet.")).toBeInTheDocument();
  });

  it("shows skeletons (not the empty message) while loading", () => {
    h.buckets.loading = true;
    render(<BucketsPage />);
    expect(screen.queryByText("No buckets yet.")).not.toBeInTheDocument();
  });

  it("renders a row per bucket with its name and backend", () => {
    h.buckets.buckets = [
      {
        backendId: "primary",
        bucketId: "acme-logs",
        displayName: "Acme Logs",
        region: "us-east-1",
        provisionState: "active",
        resourceVersion: "1",
      },
    ];
    render(<BucketsPage />);
    expect(screen.getByText("acme-logs")).toBeInTheDocument();
    expect(screen.getByText("primary")).toBeInTheDocument();
    expect(screen.queryByText("No buckets yet.")).not.toBeInTheDocument();
  });
});

// The search box drives the API, not a client-side array filter.
//
// This page used to fetch every bucket and narrow the result in the browser —
// 859 rows pulled to answer a five-character query. These pin the three things
// that changed, because all three fail silently: a filter that never leaves the
// browser looks identical on screen until the list outgrows a page, a missing
// debounce is invisible except in the network tab, and an empty table during
// the debounce reads as "your bucket is gone".
//
// fireEvent rather than userEvent: userEvent schedules its own work and does
// not resolve under vi.useFakeTimers() here — every one of these timed out at
// 5s before the switch. The debounce IS the thing under test, so the fake clock
// stays and the typing gets simpler.
describe("BucketsPage search", () => {
  const typeInSearch = (value: string) =>
    act(() => {
      fireEvent.change(screen.getByPlaceholderText(/search by name/i), {
        target: { value },
      });
    });

  const tick = (ms: number) =>
    act(() => {
      vi.advanceTimersByTime(ms);
    });

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("sends the query to the API as one CEL conjunct", () => {
    render(<BucketsPage />);
    h.buckets.fetchBuckets.mockClear();

    typeInSearch("Prod");
    tick(400);

    const calls = h.buckets.fetchBuckets.mock.calls;
    expect(calls.length).toBeGreaterThan(0);
    const [backendId, filter] = calls[calls.length - 1];
    expect(backendId).toBeUndefined();
    // Lowercased on the client to match the server's ASCII folding, and a
    // single conjunct — a disjunction would not push down to SQL, so matches
    // past the first page would come back reported as absent.
    expect(filter).toBe('search.contains("prod")');
    expect(filter).not.toContain("||");
  });

  it("debounces, so three keystrokes are not three requests", () => {
    render(<BucketsPage />);
    h.buckets.fetchBuckets.mockClear();

    // The clock MUST move between keystrokes. Typing three times and then
    // advancing once cannot tell a 300ms debounce from a 0ms one — nothing
    // fires until the clock moves either way, so the first version of this
    // test passed with the debounce set to zero.
    typeInSearch("l");
    tick(100);
    typeInSearch("lo");
    tick(100);
    typeInSearch("log");
    tick(100);
    expect(h.buckets.fetchBuckets.mock.calls.filter((c) => c[1])).toHaveLength(
      0,
    );

    tick(400);

    const filtered = h.buckets.fetchBuckets.mock.calls.filter(
      (c) => typeof c[1] === "string" && c[1] !== "",
    );
    expect(filtered).toHaveLength(1);
    expect(filtered[0][1]).toBe('search.contains("log")');
  });

  it("says it is still searching rather than that nothing matched", () => {
    render(<BucketsPage />);

    typeInSearch("prod");
    // Deliberately BEFORE the debounce fires: the table is empty because the
    // request has not been made, which is not the same as no matches.
    expect(screen.getByText("Searching…")).toBeInTheDocument();
    expect(
      screen.queryByText("No buckets match your filters."),
    ).not.toBeInTheDocument();

    tick(400);
    expect(
      screen.getByText("No buckets match your filters."),
    ).toBeInTheDocument();
  });

  it("renders what the API returned without re-filtering it", () => {
    // The server decides what matches. A page that also filtered locally would
    // hide a row the API deliberately returned — two definitions of "matches",
    // one of them invisible.
    h.buckets.buckets = [
      {
        backendId: "primary",
        bucketId: "acme-logs",
        displayName: "Acme Logs",
        region: "us-east-1",
        provisionState: "active",
        resourceVersion: "1",
      },
    ];
    render(<BucketsPage />);

    typeInSearch("zzz-no-match");
    tick(400);

    expect(screen.getByText("acme-logs")).toBeInTheDocument();
  });
});

describe("BucketsPage create dialog", () => {
  beforeEach(() => {
    h.buckets.buckets = [];
    h.backends.backends = [
      { backendId: "primary", displayName: "", features: [] },
    ];
  });

  // The dialog's own behaviour is BucketCreateDialog.test.tsx.
  it("opens the shared create dialog", () => {
    render(<BucketsPage />);
    fireEvent.click(
      screen.getByRole("button", { name: /create the first bucket/i }),
    );
    expect(screen.getByRole("dialog")).toHaveTextContent("New S3 bucket");
  });
});

describe("BucketsPage provisioning", () => {
  it("keeps asking while a bucket is still provisioning", () => {
    vi.useFakeTimers();
    try {
      h.backends.backends = [];
      h.buckets.buckets = [
        {
          backendId: "primary",
          bucketId: "fresh",
          displayName: "",
          region: "",
          provisionState: PROVISION_STATE.pending,
        },
      ];
      render(<BucketsPage />);
      h.buckets.fetchBuckets.mockClear();
      act(() => {
        vi.advanceTimersByTime(PROVISION_POLL_MS);
      });
      expect(h.buckets.fetchBuckets).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});

// ADR-0027: a public bucket is marked wherever it is listed.
describe("BucketsPage public read", () => {
  it("marks a public bucket and not a private one", () => {
    h.backends.backends = [];
    h.buckets.buckets = [
      {
        backendId: "primary",
        bucketId: "photos",
        displayName: "",
        region: "",
        provisionState: "",
        publicRead: true,
      },
      {
        backendId: "primary",
        bucketId: "docs",
        displayName: "",
        region: "",
        provisionState: "",
        publicRead: false,
      },
    ];
    render(<BucketsPage />);
    expect(screen.getAllByText("public")).toHaveLength(1);
    expect(screen.getByText("public").closest("tr")).toHaveTextContent(
      "photos",
    );
  });
});
