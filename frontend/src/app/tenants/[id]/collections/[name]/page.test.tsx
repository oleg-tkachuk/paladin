import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// A failed bucket read opened "Bind to bucket" with nothing in it and Bind
// could never enable — as if there were no bucket to bind to.
const h = vi.hoisted(() => ({
  buckets: [] as Array<Record<string, unknown>>,
  bucketsError: null as string | null,
  fetchBuckets: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({ collectionClient: {} }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: h.buckets,
    error: h.bucketsError,
    fetchBuckets: h.fetchBuckets,
  }),
}));
vi.mock("./collection-context", () => ({
  useCollection: () => ({
    collection: {
      name: "tenants/t-1/collections/docs",
      tenantId: "t-1",
      collection: "docs",
      displayName: "Docs",
      bucket: "",
      cedarPolicy: "",
    },
    setCollection: vi.fn(),
  }),
}));

import CollectionOverviewPage from "./page";

beforeEach(() => {
  h.buckets = [];
  h.bucketsError = null;
  h.fetchBuckets.mockReset();
});

describe("CollectionOverviewPage binding", () => {
  it("says the bucket list failed, and retries it", async () => {
    h.bucketsError = "unavailable: upstream";
    render(<CollectionOverviewPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    h.fetchBuckets.mockClear();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(h.fetchBuckets).toHaveBeenCalledOnce();
  });

  it("raises nothing when the list loaded", () => {
    render(<CollectionOverviewPage />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
