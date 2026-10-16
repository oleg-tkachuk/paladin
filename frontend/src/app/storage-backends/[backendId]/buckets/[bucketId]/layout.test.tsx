import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({
  getBucket: vi.fn(),
  getTenant: vi.fn(),
}));
vi.mock("next/navigation", () => ({
  useParams: () => ({ backendId: "primary", bucketId: "pool" }),
  usePathname: () => "/storage-backends/primary/buckets/pool",
  notFound: vi.fn(),
}));
vi.mock("@/components/layout/PageHeader", () => ({ PageHeader: () => null }));
vi.mock("@/components/layout/BucketTabs", () => ({
  BucketTabs: () => null,
  isBucketSettingsTab: () => false,
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { getBucket: h.getBucket },
  tenantClient: { getTenant: h.getTenant },
}));

import BucketLayout from "./layout";
import { useBucket } from "./bucket-context";
import { TRASHED_TENANT_BLOCK } from "@/app/tenants/[id]/tenant-context";

/** A tab that shows what the layout told it. */
function Probe() {
  const { changesBlocked } = useBucket();
  return <span data-testid="tab">{changesBlocked ?? "open"}</span>;
}

beforeEach(() => {
  h.getBucket.mockResolvedValue({
    name: "storageBackends/primary/buckets/pool",
    backendId: "primary",
    bucketId: "pool",
    ownerTenantId: "t-owner",
    grantedTenantIds: [],
    publicRead: false,
  });
  h.getTenant.mockResolvedValue({
    tenantId: "t-owner",
    slug: "owner",
    displayName: "Owner Co",
    deletedAt: undefined,
  });
});

describe("BucketLayout", () => {
  it("names the owner, linked to its workspace", async () => {
    render(
      <BucketLayout>
        <Probe />
      </BucketLayout>,
    );
    expect(
      await screen.findByRole("link", { name: "Owner Co" }),
    ).toHaveAttribute("href", "/tenants/owner");
    expect(h.getTenant).toHaveBeenCalledWith(
      { name: "tenants/t-owner" },
      expect.anything(),
    );
    expect(screen.getByTestId("tab")).toHaveTextContent("open");
  });

  // The bucket is frozen with its owner, whichever tenant reads it.
  it("holds changes while the owner is in the trash", async () => {
    h.getTenant.mockResolvedValue({
      tenantId: "t-owner",
      slug: "owner",
      displayName: "Owner Co",
      deletedAt: { seconds: 1n },
    });
    render(
      <BucketLayout>
        <Probe />
      </BucketLayout>,
    );
    await screen.findByRole("link", { name: "Owner Co" });
    expect(await screen.findByTestId("tab")).toHaveTextContent(
      TRASHED_TENANT_BLOCK,
    );
  });
});
