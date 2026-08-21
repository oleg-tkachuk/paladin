import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

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
