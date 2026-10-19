import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({ getBucket: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { getBucket: h.getBucket },
  tenantClient: {},
  userClient: {},
  capabilityClient: {},
  collectionClient: {},
}));

import { BucketNameLink } from "./BucketNameLink";

describe("BucketNameLink", () => {
  it("names the bucket, links to its page, and profiles it on hover", async () => {
    h.getBucket.mockResolvedValue({
      backendId: "minio",
      bucketId: "logs",
      publicRead: false,
      provisionState: "ready",
      ownerTenantId: "t-1",
      grantedTenantIds: [],
    });
    render(<BucketNameLink bucket="storageBackends/minio/buckets/logs" />);
    const link = screen.getByRole("link", { name: "logs" });
    expect(link).toHaveAttribute("href", expect.stringContaining("minio"));
    await userEvent.hover(link);
    expect(
      await screen.findByLabelText("Bucket profile", {}, { timeout: 2_000 }),
    ).toBeInTheDocument();
    await waitFor(() => expect(h.getBucket).toHaveBeenCalledTimes(1));
  });

  it("says nothing for a name that is not a bucket's", () => {
    render(<BucketNameLink bucket="" />);
    expect(screen.queryByRole("link")).toBeNull();
  });
});
