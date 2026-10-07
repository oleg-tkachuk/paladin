import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// A failed bucket read opened "Bind to bucket" with nothing in it and Bind
// could never enable — as if there were no bucket to bind to.
const h = vi.hoisted(() => ({
  // The collection's access: 2 is COLLECTION_ACCESS_PUBLIC_READ.
  access: 0,
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
      bucket: "storageBackends/primary/buckets/pub",
      cedarPolicy: "",
      access: h.access,
      cacheControl: h.access ? "public, max-age=31536000, immutable" : "",
    },
    setCollection: vi.fn(),
  }),
}));

import CollectionOverviewPage from "./page";
import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";

beforeEach(() => {
  h.access = 0;
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

// ADR-0027: a public Collection never moves — its objects' URLs name its
// bucket — so the page shows it public and offers no re-bind.
describe("CollectionOverviewPage public read", () => {
  it("shows a public Collection's access and Cache-Control, and holds the re-bind", () => {
    h.access = CollectionAccess.PUBLIC_READ;
    render(<CollectionOverviewPage />);
    expect(screen.getByText("public")).toBeInTheDocument();
    expect(
      screen.getByText("public, max-age=31536000, immutable"),
    ).toBeInTheDocument();
    expect(screen.getByText(/stays in its bucket/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Re-bind" })).toBeDisabled();
  });

  it("calls a private Collection private", () => {
    render(<CollectionOverviewPage />);
    expect(screen.getByText("private")).toBeInTheDocument();
    expect(screen.queryByText(/stays in its bucket/)).not.toBeInTheDocument();
  });
});
