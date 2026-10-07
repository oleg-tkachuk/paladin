import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// Characterization net for the tenant buckets page. No decomposition yet — this
// pins the observable behaviors (header + create action, the tenant-scoped
// fetch on mount, the create dialog wiring, and delete via the row menu) so a
// later refactor has a safety floor. Mocks the data hooks + tenant context +
// notifications; useTableSort is a pure hook and stays real.
const h = vi.hoisted(() => ({
  buckets: [] as Array<Record<string, unknown>>,
  bucketsError: null as string | null,
  fetchBuckets: vi.fn(),
  createBucket: vi.fn(() => Promise.resolve()),
  deleteBucket: vi.fn(() => Promise.resolve()),
  showNotification: vi.fn(),
}));

vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: h.buckets,
    loading: false,
    error: h.bucketsError,
    fetchBuckets: h.fetchBuckets,
    createBucket: h.createBucket,
    deleteBucket: h.deleteBucket,
  }),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: [
      { backendId: "be-1", features: [], enabled: true, declared: true },
    ],
  }),
}));
vi.mock("../tenant-context", () => ({
  useTenantChangesBlocked: () => null,
  useTenant: () => ({ tenantId: "t-1", displayName: "Acme" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import TenantBucketsPage from "./page";
import { PROVISION_POLL_MS } from "@/lib/bucketProvision";

const makeBucket = (name: string) => ({
  backendId: "be-1",
  bucketId: name,
  displayName: "",
  region: "us-east-1",
  provisionState: "PROVISION_STATE_READY",
  resourceVersion: "v1",
});

beforeEach(() => {
  h.buckets = [];
  h.bucketsError = null;
  h.fetchBuckets.mockClear();
  h.createBucket.mockClear();
  h.deleteBucket.mockClear();
  h.showNotification.mockClear();
});

describe("TenantBucketsPage", () => {
  it("renders the header and a create action", () => {
    render(<TenantBucketsPage />);
    expect(
      screen.getByRole("heading", { name: "Buckets" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /New bucket/i }),
    ).toBeInTheDocument();
  });

  it("fetches this tenant's buckets on mount", () => {
    render(<TenantBucketsPage />);
    expect(h.fetchBuckets).toHaveBeenCalledWith(undefined, "", "", "t-1");
  });

  // Refresh called fetchBuckets() bare, which lists every tenant's buckets.
  it("refreshes this tenant's buckets, not everyone's", () => {
    render(<TenantBucketsPage />);
    h.fetchBuckets.mockClear();
    fireEvent.click(screen.getByRole("button", { name: /refresh/i }));
    expect(h.fetchBuckets).toHaveBeenCalledWith(undefined, "", "", "t-1");
  });

  // A bucket settles in the reconciler; the list asks again until it has.
  it("keeps asking while a bucket is still provisioning", () => {
    vi.useFakeTimers();
    try {
      h.buckets = [{ ...makeBucket("fresh"), provisionState: "pending" }];
      render(<TenantBucketsPage />);
      h.fetchBuckets.mockClear();
      vi.advanceTimersByTime(PROVISION_POLL_MS);
      expect(h.fetchBuckets).toHaveBeenCalledWith(undefined, "", "", "t-1");
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not poll a settled list", () => {
    vi.useFakeTimers();
    try {
      h.buckets = [{ ...makeBucket("done"), provisionState: "ready" }];
      render(<TenantBucketsPage />);
      h.fetchBuckets.mockClear();
      vi.advanceTimersByTime(PROVISION_POLL_MS * 2);
      expect(h.fetchBuckets).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("opens the create dialog from the header action", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("paladin-primary")).toBeInTheDocument();
  });

  it("does not create when the bucket name is empty", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    expect(h.createBucket).not.toHaveBeenCalled();
  });

  it("creates a bucket when a name is provided", async () => {
    render(<TenantBucketsPage />);
    await userEvent.click(screen.getByRole("button", { name: /New bucket/i }));
    await userEvent.type(
      screen.getByPlaceholderText("paladin-primary"),
      "my-bucket",
    );
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() =>
      expect(h.createBucket).toHaveBeenCalledWith(
        "be-1",
        "my-bucket",
        "",
        "",
        true,
        null,
      ),
    );
  });

  it("deletes a bucket from the row menu", async () => {
    h.buckets = [makeBucket("my-bucket")];
    // Radix sets pointer-events:none on the body while the menu/overlay is
    // open; userEvent's default pointer-events check would then refuse the
    // follow-up clicks. Disable it for this open-menu → confirm flow.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TenantBucketsPage />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for my-bucket" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: /Delete bucket/i }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Delete bucket" }),
    );
    await waitFor(() =>
      expect(h.deleteBucket).toHaveBeenCalledWith(
        "be-1",
        "my-bucket",
        "v1",
        false,
      ),
    );
  });

  // A failed read rendered "No buckets owned by this tenant yet." with an
  // offer to create one.
  it("says the bucket list failed, not that the tenant owns none", async () => {
    h.bucketsError = "unavailable: upstream";
    render(<TenantBucketsPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(
      screen.queryByText("No buckets owned by this tenant yet."),
    ).not.toBeInTheDocument();
    h.fetchBuckets.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    // Retry asks for the same tenant-scoped list the page loaded.
    expect(h.fetchBuckets).toHaveBeenCalledWith(undefined, "", "", "t-1");
  });
});
