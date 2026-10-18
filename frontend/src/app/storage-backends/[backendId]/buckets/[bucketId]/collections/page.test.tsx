import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

// A bucket's Collections tab: what each tenant stores in it, by tenant, each
// Collection linking to its one page in its tenant's workspace (ADR-0033).
const h = vi.hoisted(() => ({
  listCollections: vi.fn(),
  fetchTenants: vi.fn(),
  tenants: [] as Array<{ tenantId: string; slug: string; displayName: string }>,
}));
vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: h.tenants,
    error: null,
    fetchTenants: h.fetchTenants,
  }),
}));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: { listCollections: h.listCollections },
}));
vi.mock("../bucket-context", () => ({
  useBucket: () => ({
    bucket: { name: "storageBackends/primary/buckets/pool" },
  }),
}));

import BucketCollectionsPage from "./page";

beforeEach(() => {
  h.tenants = [
    { tenantId: "t-a", slug: "tenant-a", displayName: "Tenant A" },
    { tenantId: "t-b", slug: "tenant-b", displayName: "" },
  ];
  h.listCollections.mockResolvedValue({
    collections: [
      { tenantId: "t-a", collection: "docs", displayName: "" },
      { tenantId: "t-a", collection: "media", displayName: "Media" },
      { tenantId: "t-b", collection: "logs", displayName: "" },
    ],
  });
});

describe("BucketCollectionsPage", () => {
  it("reads only this bucket's collections", async () => {
    render(<BucketCollectionsPage />);
    await screen.findByText("docs");
    expect(h.listCollections).toHaveBeenCalledWith(
      expect.objectContaining({
        bucket: "storageBackends/primary/buckets/pool",
      }),
      expect.anything(),
    );
  });

  it("groups them by tenant, counted as collections", async () => {
    render(<BucketCollectionsPage />);
    expect(
      await screen.findByRole("link", { name: /Tenant A/ }),
    ).toHaveAttribute("href", "/tenants/tenant-a");
    expect(screen.getByText(/2 collections/)).toBeInTheDocument();
    expect(screen.getByText(/1 collection$/)).toBeInTheDocument();
  });

  it("links each collection to its page in its tenant's workspace", async () => {
    render(<BucketCollectionsPage />);
    expect(await screen.findByRole("link", { name: "docs" })).toHaveAttribute(
      "href",
      "/tenants/tenant-a/collections/docs",
    );
  });

  it("shows every collection of a tenant the search names", async () => {
    render(<BucketCollectionsPage />);
    await screen.findByText("docs");
    fireEvent.change(
      screen.getByRole("searchbox", {
        name: "Search this bucket's collections",
      }),
      { target: { value: "tenant-a" } },
    );
    // The tenant matched, not its collections' names: both still show.
    expect(screen.getByText("docs")).toBeInTheDocument();
    expect(screen.getByText("media")).toBeInTheDocument();
    expect(screen.queryByText("logs")).toBeNull();
    expect(screen.getByText("2 of 3")).toBeInTheDocument();
  });

  it("sorts the collections inside each tenant", async () => {
    render(<BucketCollectionsPage />);
    await screen.findByText("docs");
    const tenantA = () =>
      screen
        .getAllByRole("link")
        .map((l) => l.textContent)
        .filter((t) => t === "docs" || t === "media");
    const [header] = screen.getAllByRole("button", { name: /^Collection/ });
    fireEvent.click(header);
    fireEvent.click(header);
    expect(tenantA()).toEqual(["media", "docs"]);
  });

  it("says nothing is bound when the bucket holds no collection", async () => {
    h.listCollections.mockResolvedValue({ collections: [] });
    render(<BucketCollectionsPage />);
    expect(
      await screen.findByText("No collection is bound to this bucket yet."),
    ).toBeInTheDocument();
  });
});
