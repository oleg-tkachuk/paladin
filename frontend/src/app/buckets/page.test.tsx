import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Monolith list-page smoke pattern: mock the data hooks + the shell pieces
// (PageHeader pulls scope context) and assert the three render branches —
// loading, empty, and a populated row. This is the regression net that
// guards decomposing this page later.
const h = vi.hoisted(() => ({
  // Every read the page handed useBuckets, in render order.
  reads: [] as unknown[],
  buckets: {
    buckets: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    refetch: vi.fn(),
    fetched: true,
    createBucket: vi.fn(),
    deleteBucket: vi.fn(),
  },
  backends: { backends: [] as Array<Record<string, unknown>> },
  tenants: {
    tenants: [] as Array<Record<string, unknown>>,
    error: null as string | null,
    refetch: vi.fn(),
  },
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
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: (read: unknown) => {
    h.reads.push(read);
    return h.buckets;
  },
}));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));
vi.mock("@/hooks/useTenants", () => ({ useTenants: () => h.tenants }));

import BucketsPage from "./page";
import { PROVISION_POLL_MS, PROVISION_STATE } from "@/lib/bucketProvision";

type ListRead = { backendId?: string; filter?: string };

/** The distinct filters the page read, in order. */
const filtersRead = () =>
  (h.reads as ListRead[])
    .map((r) => r.filter ?? "")
    .filter((f, i, all) => i === 0 || f !== all[i - 1]);

beforeEach(() => {
  h.reads = [];
  h.buckets.buckets = [];
  h.buckets.loading = false;
  h.buckets.refetch.mockClear();
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

  it("renders a row per bucket with its name and display name", () => {
    h.buckets.buckets = [
      {
        backendId: "primary",
        bucketId: "acme-logs",
        grantedTenantIds: [] as string[],
        displayName: "Acme Logs",
        region: "us-east-1",
        provisionState: "active",
        resourceVersion: "1",
      },
    ];
    render(<BucketsPage />);
    expect(screen.getByText("acme-logs")).toBeInTheDocument();
    expect(screen.getByText("Acme Logs")).toBeInTheDocument();
    expect(screen.queryByText("No buckets yet.")).not.toBeInTheDocument();
  });

  // ADR-0033: a bucket's settings are tabs of its one page, under its
  // backend, not of a copy under its owner's workspace.
  it("links each bucket's lifecycle rules on the bucket's page", async () => {
    h.buckets.buckets = [
      {
        backendId: "primary",
        bucketId: "pool",
        grantedTenantIds: [] as string[],
        displayName: "",
        region: "",
        ownerTenantId: "t-platform",
        provisionState: "ready",
        resourceVersion: "1",
      },
    ];
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<BucketsPage />);
    await user.click(screen.getByRole("button", { name: "Actions for pool" }));
    expect(
      await screen.findByRole("menuitem", { name: /Lifecycle rules/ }),
    ).toHaveAttribute(
      "href",
      "/storage-backends/primary/buckets/pool/lifecycle",
    );
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

    typeInSearch("Prod");
    tick(400);

    const { backendId, filter } = h.reads.at(-1) as ListRead;
    expect(backendId).toBeUndefined();
    // Lowercased on the client to match the server's ASCII folding, and a
    // single conjunct — a disjunction would not push down to SQL, so matches
    // past the first page would come back reported as absent.
    expect(filter).toBe('search.contains("prod")');
    expect(filter).not.toContain("||");
  });

  it("debounces, so three keystrokes are not three requests", () => {
    render(<BucketsPage />);

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
    expect(filtersRead().filter((f) => f !== "")).toHaveLength(0);

    tick(400);

    // Only the final query reached the hook; "l" and "lo" never did.
    expect(filtersRead().filter((f) => f !== "")).toEqual([
      'search.contains("log")',
    ]);
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
        grantedTenantIds: [] as string[],
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
  it("opens the create dialog with an owner picker", () => {
    render(<BucketsPage />);
    fireEvent.click(
      screen.getByRole("button", { name: /create the first bucket/i }),
    );
    expect(screen.getByRole("dialog")).toHaveTextContent("New S3 bucket");
    // ADR-0032: every bucket has an owner; this page lets the operator pick.
    expect(
      screen.getByRole("combobox", { name: /Owner tenant/ }),
    ).toBeInTheDocument();
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
          grantedTenantIds: [] as string[],
          displayName: "",
          region: "",
          provisionState: PROVISION_STATE.pending,
        },
      ];
      render(<BucketsPage />);
      expect(h.buckets.refetch).not.toHaveBeenCalled();
      act(() => {
        vi.advanceTimersByTime(PROVISION_POLL_MS);
      });
      expect(h.buckets.refetch).toHaveBeenCalled();
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
        grantedTenantIds: [] as string[],
        displayName: "",
        region: "",
        provisionState: "",
        publicRead: true,
      },
      {
        backendId: "primary",
        bucketId: "docs",
        grantedTenantIds: [] as string[],
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

describe("BucketsPage sharing", () => {
  const shared = {
    backendId: "primary",
    bucketId: "pool",
    grantedTenantIds: ["t-a"],
    displayName: "",
    region: "",
    ownerTenantId: "t-platform",
    provisionState: "ready",
    resourceVersion: "1",
  };
  const alone = { ...shared, bucketId: "solo", grantedTenantIds: [] };

  beforeEach(() => {
    h.buckets.buckets = [shared, alone];
  });

  // ADR-0032 made who a bucket is shared with the fact its settings reach;
  // the list can be narrowed by it.
  it("narrows the list to the shared buckets, and to the owner-only ones", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<BucketsPage />);
    expect(screen.getByText("pool")).toBeInTheDocument();
    expect(screen.getByText("solo")).toBeInTheDocument();

    await user.click(
      screen.getByRole("combobox", { name: "Filter by sharing" }),
    );
    await user.click(await screen.findByRole("option", { name: "Shared" }));
    expect(screen.getByText("pool")).toBeInTheDocument();
    expect(screen.queryByText("solo")).not.toBeInTheDocument();

    await user.click(
      screen.getByRole("combobox", { name: "Filter by sharing" }),
    );
    await user.click(await screen.findByRole("option", { name: "Owner only" }));
    expect(screen.queryByText("pool")).not.toBeInTheDocument();
    expect(screen.getByText("solo")).toBeInTheDocument();
  });
});
