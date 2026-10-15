import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

// ADR-0027: a public bucket is marked as such on its overview, with where it
// is served from and the content types it may serve.
const h = vi.hoisted(() => ({
  publicRead: false,
  publicBaseUrl: "",
  allowedContentTypes: [] as string[],
}));

vi.mock("../../../tenant-context", () => ({
  useTenant: () => ({ slug: "acme" }),
}));
vi.mock("./bucket-context", () => ({
  useBucket: () => ({
    bucket: {
      name: "storageBackends/primary/buckets/assets",
      backendId: "primary",
      bucketId: "assets",
      displayName: "Assets",
      region: "",
      ownerTenantId: "t-1",
      lifecycleRules: [],
      cedarPolicy: "",
      provisionState: "ready",
      resourceVersion: "3",
      publicRead: h.publicRead,
      publicBaseUrl: h.publicBaseUrl,
      constraints: { allowedContentTypes: h.allowedContentTypes },
    },
    setBucket: vi.fn(),
    refetch: vi.fn(),
  }),
}));

import BucketOverviewPage from "./page";

beforeEach(() => {
  h.publicRead = false;
  h.publicBaseUrl = "";
  h.allowedContentTypes = [];
});

describe("BucketOverviewPage public read", () => {
  it("shows a private bucket as private, with no public fields", () => {
    render(<BucketOverviewPage />);
    expect(screen.getByText("private")).toBeInTheDocument();
    expect(screen.queryByText("public")).not.toBeInTheDocument();
    expect(screen.queryByText("Public base URL")).not.toBeInTheDocument();
    expect(screen.queryByText("Allowed content types")).not.toBeInTheDocument();
  });

  it("marks a public bucket and shows its base URL and content types", () => {
    h.publicRead = true;
    h.publicBaseUrl = "https://cdn.example.com";
    h.allowedContentTypes = ["image/png", "image/jpeg"];
    render(<BucketOverviewPage />);
    expect(screen.getByText("public")).toBeInTheDocument();
    expect(screen.getByText("https://cdn.example.com")).toBeInTheDocument();
    expect(screen.getByText("image/png, image/jpeg")).toBeInTheDocument();
  });

  it("says a public bucket without a base URL is served from the backend", () => {
    h.publicRead = true;
    render(<BucketOverviewPage />);
    expect(
      screen.getByText("the backend's public endpoint"),
    ).toBeInTheDocument();
  });
});

// ADR-0032: every bucket has an owner, and its grants are listed on the
// storage-first bucket page.
describe("BucketOverviewPage ownership", () => {
  it("names the owner tenant and never calls the bucket ownerless", () => {
    render(<BucketOverviewPage />);
    expect(screen.getByText("t-1")).toBeInTheDocument();
    expect(screen.queryByText(/no owner/)).not.toBeInTheDocument();
  });

  it("links to where the bucket's grants are listed", () => {
    render(<BucketOverviewPage />);
    expect(screen.getByRole("link", { name: /Sharing/ })).toHaveAttribute(
      "href",
      "/storage-backends/primary/buckets/assets",
    );
  });
});
