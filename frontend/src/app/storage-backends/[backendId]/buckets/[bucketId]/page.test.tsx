import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

// The storage-first bucket page names the bucket's owner and lists the tenants
// it is granted to (ADR-0032). The grants card's own behaviour is
// BucketGrantsCard.test.tsx.
const h = vi.hoisted(() => ({
  getBucket: vi.fn(),
  listCollections: vi.fn(),
  listBucketGrants: vi.fn(),
  fetchTenants: vi.fn(),
  tenants: [] as Array<{ tenantId: string; slug: string; displayName: string }>,
}));
vi.mock("next/navigation", () => ({
  useParams: () => ({ backendId: "primary", bucketId: "pool" }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/storage-backends/primary/buckets/pool",
  useSearchParams: () => new URLSearchParams(""),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({ backends: [] }),
}));
vi.mock("@/hooks/useTenants", () => ({
  useTenants: () => ({
    tenants: h.tenants,
    error: null,
    fetchTenants: h.fetchTenants,
  }),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: {
    getBucket: h.getBucket,
    listBucketGrants: h.listBucketGrants,
  },
  collectionClient: { listCollections: h.listCollections },
}));

import BucketDetailPage from "./page";

const OWNER = "t-platform";

beforeEach(() => {
  h.tenants = [];
  h.getBucket.mockResolvedValue({
    name: "storageBackends/primary/buckets/pool",
    backendId: "primary",
    bucketId: "pool",
    displayName: "",
    region: "",
    ownerTenantId: OWNER,
  });
  h.listCollections.mockResolvedValue({ collections: [] });
  h.listBucketGrants.mockResolvedValue({
    grants: [{ bucket: "", tenantId: "t-acme", grantedBy: "admin" }],
  });
});

describe("BucketDetailPage ownership", () => {
  it("names the owner tenant, linking to it once the tenant list resolves it", async () => {
    h.tenants = [
      { tenantId: OWNER, slug: "platform", displayName: "Platform" },
      { tenantId: "t-acme", slug: "acme", displayName: "Acme" },
    ];
    render(<BucketDetailPage />);
    expect(
      await screen.findByRole("link", { name: "Platform (platform)" }),
    ).toHaveAttribute("href", "/tenants/platform");
  });

  it("shows the owner's UUID while the tenant is not resolved", async () => {
    render(<BucketDetailPage />);
    expect(await screen.findByText(OWNER)).toBeInTheDocument();
  });

  it("lists the tenants the bucket is shared with", async () => {
    h.tenants = [{ tenantId: "t-acme", slug: "acme", displayName: "Acme" }];
    render(<BucketDetailPage />);
    expect(await screen.findByText("Shared with")).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: /Acme/ })).toHaveAttribute(
      "href",
      "/tenants/acme",
    );
    expect(h.listBucketGrants).toHaveBeenCalledWith({
      name: "storageBackends/primary/buckets/pool",
    });
  });
});
