import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

// A failed backend or bucket read used to render as a claim about the data:
// "Backend not found." for a backend that may exist, and "No buckets on this
// backend yet." with a count of 0 and an offer to create one.
const h = vi.hoisted(() => ({
  backends: {
    backends: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    fetchBackends: vi.fn(),
  },
  buckets: {
    buckets: [] as Array<Record<string, unknown>>,
    loading: false,
    error: null as string | null,
    fetchBuckets: vi.fn(),
  },
}));

vi.mock("next/navigation", () => ({
  useParams: () => ({ backendId: "primary" }),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("./BackendActions", () => ({ BackendActions: () => null }));
vi.mock("@/hooks/useBackends", () => ({ useBackends: () => h.backends }));
vi.mock("@/hooks/useBuckets", () => ({ useBuckets: () => h.buckets }));

import StorageBackendDetailPage from "./page";

// features and compatibility as the server sends them for a backend never probed.
const PRIMARY = {
  backendId: "primary",
  displayName: "Primary",
  kind: 0,
  features: [],
  compatibility: 0,
};

beforeEach(() => {
  h.backends.backends = [PRIMARY];
  h.backends.error = null;
  h.buckets.buckets = [];
  h.buckets.error = null;
});

describe("StorageBackendDetailPage", () => {
  it("says the backend list failed, not that the backend does not exist", () => {
    h.backends.backends = [];
    h.backends.error = "unavailable: upstream";
    render(<StorageBackendDetailPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Backends could not be loaded/,
    );
    expect(screen.queryByText("Backend not found.")).not.toBeInTheDocument();
  });

  it("says the bucket list failed, not that the backend has none", () => {
    h.buckets.error = "unavailable: upstream";
    render(<StorageBackendDetailPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(
      screen.queryByText("No buckets on this backend yet."),
    ).not.toBeInTheDocument();
    // The count is unknown, not zero.
    expect(
      screen.getByRole("heading", { name: /Buckets on this backend/ }),
    ).toHaveTextContent(/Buckets on this backend\s*—/);
  });

  it("still says so when the backend really has no buckets", () => {
    render(<StorageBackendDetailPage />);
    expect(
      screen.getByText("No buckets on this backend yet."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
