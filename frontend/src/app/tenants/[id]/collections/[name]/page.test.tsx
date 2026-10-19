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
  // Every read the page handed useBuckets.
  reads: [] as unknown[],
  refetch: vi.fn(),
}));

vi.mock("@/lib/connect/client", () => ({ collectionClient: {} }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: (read: unknown) => {
    h.reads.push(read);
    return {
      buckets: h.buckets,
      error: h.bucketsError,
      loading: false,
      refetch: h.refetch,
      fetched: true,
      createBucket: vi.fn(),
      deleteBucket: vi.fn(),
    };
  },
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
  h.reads = [];
  h.refetch.mockReset();
});

describe("CollectionOverviewPage binding", () => {
  it("says the bucket list failed, and retries it", async () => {
    h.bucketsError = "unavailable: upstream";
    render(<CollectionOverviewPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(h.refetch).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(h.refetch).toHaveBeenCalledOnce();
  });

  // ADR-0032: Bind is refused for a bucket the tenant neither owns nor was
  // granted, so the list offers only the ones it may bind to.
  it("offers the buckets the collection's tenant may bind to", () => {
    render(<CollectionOverviewPage />);
    expect(h.reads.length).toBeGreaterThan(0);
    for (const read of h.reads) {
      expect(read).toEqual({ accessibleTo: "tenants/t-1" });
    }
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
